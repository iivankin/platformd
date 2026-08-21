import { createHash, randomUUID } from "node:crypto";
import { spawnSync } from "node:child_process";
import { appendFileSync, createReadStream, readFileSync, statSync } from "node:fs";
import type { Readable } from "node:stream";
import { Transform } from "node:stream";
import { collapsePreview, formatError } from "./errors.js";

const terminalStatuses = new Set(["failed", "succeeded", "superseded"]);

// Cloudflare proxies reject request bodies over 100 MiB. Parts stay far below
// that so several services can upload at once without each POST sitting in the
// proxy for the 125s read / 30s origin-write timeouts.
const CLOUDFLARE_MAX_BODY_BYTES = 100 * 1024 * 1024;
const DEFAULT_CHUNK_BYTES = 8 * 1024 * 1024;
const DEFAULT_CONCURRENCY = 2;
const MAX_CONCURRENCY = 8;
const PART_ATTEMPTS = 6;
const PART_REQUEST_TIMEOUT_MS = 110_000;
const PART_RETRY_BASE_MS = 1_000;
const OIDC_REFRESH_SKEW_MS = 60_000;
const PROGRESS_INTERVAL_MS = 400;
const MAX_COMMIT_MESSAGE_BYTES = 512;

type JSONObject = Record<string, unknown>;

type UploadStatus = {
  offset?: number;
  status?: string;
  errorMessage?: string;
  errorCode?: string;
  projectId?: string;
  serviceId?: string;
  deploymentId?: string;
  previewId?: string;
  url?: string;
  digest?: string;
};

type CachedOIDCToken = {
  audience: string;
  expiresAtMs: number;
  token: string;
};

let cachedOIDCToken: CachedOIDCToken | undefined;

function input(name: string, fallback = ""): string {
  const key = `INPUT_${name.replaceAll(" ", "_").toUpperCase()}`;
  const raw = process.env[key];
  // GitHub sets INPUT_* to "" for omitted optional inputs; treat that as unset.
  if (raw === undefined) {
    return fallback.trim();
  }
  const value = raw.trim();
  return value === "" ? fallback.trim() : value;
}

function sleep(milliseconds: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, milliseconds));
}

function normalizeCommitMessage(value: unknown): string {
  if (typeof value !== "string") {
    return "";
  }
  const subject = value.split(/\r?\n/u, 1)[0]?.trim() ?? "";
  let result = "";
  let bytes = 0;
  for (const character of subject) {
    const characterBytes = Buffer.byteLength(character, "utf8");
    if (bytes + characterBytes > MAX_COMMIT_MESSAGE_BYTES) {
      break;
    }
    result += character;
    bytes += characterBytes;
  }
  return result;
}

function commitMessage(): string {
  const sha = process.env.GITHUB_SHA;
  if (sha) {
    const git = spawnSync("git", ["show", "-s", "--format=%s", sha], {
      cwd: process.env.GITHUB_WORKSPACE || undefined,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "ignore"],
    });
    if (git.status === 0) {
      const subject = normalizeCommitMessage(git.stdout);
      if (subject) {
        return subject;
      }
    }
  }

  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (!eventPath) {
    return "";
  }
  try {
    const event = JSON.parse(readFileSync(eventPath, "utf8")) as {
      head_commit?: { message?: unknown };
      workflow_run?: { head_commit?: { message?: unknown } };
    };
    return normalizeCommitMessage(
      event.head_commit?.message ?? event.workflow_run?.head_commit?.message,
    );
  } catch {
    return "";
  }
}

function setOutput(name: string, value: string | undefined): void {
  if (!value) {
    return;
  }
  const output = process.env.GITHUB_OUTPUT;
  if (!output) {
    throw new Error("GITHUB_OUTPUT is not available");
  }
  appendFileSync(output, `${name}=${String(value).replaceAll(/[\r\n]/gu, "")}\n`);
}

function jwtExpiryMs(token: string): number | undefined {
  const parts = token.split(".");
  if (parts.length < 2 || !parts[1]) {
    return undefined;
  }
  try {
    const payload = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8")) as {
      exp?: unknown;
    };
    return typeof payload.exp === "number" ? payload.exp * 1000 : undefined;
  } catch {
    return undefined;
  }
}

async function oidcToken(audience: string): Promise<string> {
  const now = Date.now();
  if (
    cachedOIDCToken &&
    cachedOIDCToken.audience === audience &&
    cachedOIDCToken.expiresAtMs - OIDC_REFRESH_SKEW_MS > now
  ) {
    return cachedOIDCToken.token;
  }
  const requestURL = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
  const requestToken = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  if (!(requestURL && requestToken)) {
    throw new Error(
      "GitHub OIDC is unavailable. Add `permissions: id-token: write` to the workflow.",
    );
  }
  const url = new URL(requestURL);
  url.searchParams.set("audience", audience);
  const response = await fetchOrThrow("GitHub OIDC token request", url, {
    headers: { Authorization: `Bearer ${requestToken}` },
  });
  if (!response.ok) {
    throw new Error(`GitHub OIDC token request failed with ${response.status}`);
  }
  const body = (await response.json()) as { value?: unknown };
  if (typeof body.value !== "string" || !body.value) {
    throw new Error("GitHub OIDC token response did not contain a token");
  }
  cachedOIDCToken = {
    audience,
    expiresAtMs: jwtExpiryMs(body.value) ?? now + 5 * 60_000,
    token: body.value,
  };
  return body.value;
}

function parseChunkSize(raw: string): number {
  const chunkSize = Number(raw);
  if (!Number.isSafeInteger(chunkSize) || chunkSize <= 0) {
    throw new Error("chunk-size must be a positive integer");
  }
  if (chunkSize > CLOUDFLARE_MAX_BODY_BYTES) {
    throw new Error(
      `chunk-size must be <= ${CLOUDFLARE_MAX_BODY_BYTES} bytes (Cloudflare proxied body limit)`,
    );
  }
  return chunkSize;
}

function parseConcurrency(raw: string): number {
  const concurrency = Number(raw);
  if (!Number.isSafeInteger(concurrency) || concurrency < 1) {
    throw new Error("concurrency must be a positive integer");
  }
  return Math.min(concurrency, MAX_CONCURRENCY);
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) {
    return `${bytes}B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)}KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}

function formatRate(bytesPerSecond: number): string {
  return `${formatBytes(bytesPerSecond)}/s`;
}

class ProgressRenderer {
  private readonly tty: boolean;
  private stepStartedAt = Date.now();
  private lastWriteAt = 0;
  private activeLine = false;

  constructor(private readonly stream: NodeJS.WriteStream = process.stderr) {
    this.tty = Boolean(stream.isTTY);
  }

  start(step: number, title: string): void {
    this.finishLine();
    this.stepStartedAt = Date.now();
    this.stream.write(`#${step} ${title}\n`);
  }

  update(step: number, verb: string, detail: string, force = false): void {
    const now = Date.now();
    if (!force && !this.tty && now - this.lastWriteAt < PROGRESS_INTERVAL_MS) {
      return;
    }
    this.lastWriteAt = now;
    const line = `#${step} ${verb.padEnd(10)} ${detail}`;
    if (this.tty) {
      this.stream.write(`\r${line}`);
      this.stream.clearLine?.(1);
      this.activeLine = true;
      return;
    }
    this.stream.write(`${line}\n`);
  }

  done(step: number, verb: string, detail: string): void {
    const elapsed = ((Date.now() - this.stepStartedAt) / 1000).toFixed(1);
    this.finishLine();
    this.stream.write(`#${step} ${verb.padEnd(10)} ${detail}  ${elapsed}s done\n`);
  }

  status(step: number, status: string): void {
    this.finishLine();
    this.stream.write(`#${step} ${status}\n`);
  }

  private finishLine(): void {
    if (this.activeLine) {
      this.stream.write("\n");
      this.activeLine = false;
    }
  }
}

type UploadPart = {
  offset: number;
  length: number;
};

function splitParts(totalSize: number, chunkSize: number): UploadPart[] {
  const parts: UploadPart[] = [];
  for (let offset = 0; offset < totalSize; offset += chunkSize) {
    parts.push({
      offset,
      length: Math.min(chunkSize, totalSize - offset),
    });
  }
  return parts;
}

function countingStream(
  source: Readable,
  onBytes: (bytes: number) => void,
): Transform {
  const transform = new Transform({
    transform(chunk, _encoding, callback) {
      onBytes(chunk.length);
      callback(null, chunk);
    },
  });
  source.on("error", (error) => transform.destroy(error));
  source.pipe(transform);
  return transform;
}

async function mapPool<T>(
  items: T[],
  concurrency: number,
  worker: (item: T) => Promise<void>,
): Promise<void> {
  let next = 0;
  const runners = Array.from({ length: Math.min(concurrency, items.length) }, async () => {
    while (true) {
      const index = next;
      next += 1;
      if (index >= items.length) {
        return;
      }
      await worker(items[index] as T);
    }
  });
  await Promise.all(runners);
}

async function fetchOrThrow(
  label: string,
  input: string | URL,
  init?: RequestInit,
): Promise<Response> {
  try {
    return await fetch(input, init);
  } catch (error) {
    throw new Error(`${label} failed`, { cause: error });
  }
}

function uploadRequestLabel(method: string, headers: Record<string, string>): string {
  const id = headers["Upload-ID"];
  if (method === "GET") {
    return id ? `status upload=${id}` : "status";
  }
  const offset = headers["Upload-Offset"] ?? "?";
  const length = headers["Content-Length"] ?? "?";
  return id
    ? `part upload=${id} offset=${offset} length=${length}`
    : `part offset=${offset} length=${length}`;
}

function responseDiagnostics(response: Response): string {
  const extras = [`status=${response.status}`];
  const ray = response.headers.get("cf-ray");
  if (ray) {
    extras.push(`cf-ray=${ray}`);
  }
  return extras.join(" ");
}

async function responseBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) {
    return {};
  }
  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new Error(
      `invalid JSON ${responseDiagnostics(response)} body=${collapsePreview(text)}`,
    );
  }
}

function isRetryableUploadError(error: unknown): boolean {
  const text = formatError(error);
  const statusMatch = /\bstatus=(\d{3})\b/u.exec(text);
  if (statusMatch) {
    const status = Number(statusMatch[1]);
    if (
      status >= 400 &&
      status < 500 &&
      ![408, 409, 425, 429].includes(status)
    ) {
      return false;
    }
  }
  if (
    text.includes("invalid_oidc") ||
    text.includes("chunk_too_large") ||
    text.includes("chunk_exceeds") ||
    text.includes("range_overlap") ||
    text.includes("upload_changed")
  ) {
    return false;
  }
  return true;
}

async function request(
  endpoint: string,
  audience: string,
  method: string,
  headers: Record<string, string>,
  body: Readable | undefined,
  timeoutMs?: number,
): Promise<UploadStatus> {
  const token = await oidcToken(audience);
  const label = `platformd ${method} ${uploadRequestLabel(method, headers)}`;
  const response = await fetchOrThrow(label, endpoint, {
    body,
    duplex: body ? "half" : undefined,
    headers: {
      Accept: "application/json",
      Authorization: `Bearer ${token}`,
      ...headers,
    },
    method,
    signal: timeoutMs ? AbortSignal.timeout(timeoutMs) : undefined,
  } as RequestInit);
  let payload: unknown;
  try {
    payload = await responseBody(response);
  } catch (error) {
    throw new Error(`${label} failed`, { cause: error });
  }
  if (!response.ok) {
    const error =
      payload && typeof payload === "object"
        ? (payload as { error?: { code?: unknown; message?: unknown } }).error
        : undefined;
    const code = typeof error?.code === "string" ? error.code : "";
    const detail =
      typeof error?.message === "string" && error.message
        ? error.message
        : response.statusText || `HTTP ${response.status}`;
    const summary = [code, detail].filter(Boolean).join(": ");
    throw new Error(`${label} failed: ${summary} (${responseDiagnostics(response)})`);
  }
  return payload as UploadStatus;
}

function sha256File(path: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const hash = createHash("sha256");
    const stream = createReadStream(path);
    stream.on("error", reject);
    stream.on("data", (chunk: Buffer | string) => hash.update(chunk));
    stream.on("end", () => resolve(hash.digest("hex")));
  });
}

class GitHubAPIError extends Error {
  readonly status: number;

  constructor(path: string, status: number, detail: string) {
    super(`GitHub API ${path} failed: ${detail}`);
    this.name = "GitHubAPIError";
    this.status = status;
  }
}

async function githubAPI(
  token: string,
  method: string,
  path: string,
  body?: JSONObject,
): Promise<unknown> {
  const response = await fetchOrThrow(`GitHub API ${method} ${path}`, `https://api.github.com${path}`, {
    method,
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      "X-GitHub-Api-Version": "2022-11-28",
      "User-Agent": "platformd-upload-image",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const payload = await responseBody(response);
  if (!response.ok) {
    const detail =
      payload && typeof payload === "object" && typeof (payload as JSONObject).message === "string"
        ? ((payload as JSONObject).message as string)
        : `HTTP ${response.status}`;
    throw new GitHubAPIError(path, response.status, detail);
  }
  return payload;
}

async function githubAPIPages(token: string, path: string): Promise<JSONObject[]> {
  const results: JSONObject[] = [];
  for (let page = 1; ; page += 1) {
    const separator = path.includes("?") ? "&" : "?";
    const payload = await githubAPI(
      token,
      "GET",
      `${path}${separator}per_page=100&page=${page}`,
    );
    if (!Array.isArray(payload)) {
      throw new Error(`GitHub API ${path} did not return a list`);
    }
    const batch = payload as JSONObject[];
    results.push(...batch);
    if (batch.length < 100) {
      return results;
    }
  }
}

function defaultEnvironmentName(resource: string, tag: string): string {
  if (tag === "latest") {
    return resource;
  }
  const safe = tag.replaceAll(/[^A-Za-z0-9._-]/gu, "-").replaceAll(/-+/gu, "-");
  return `${resource}/preview-${safe}`.slice(0, 255);
}

function platformdLogsURL(
  endpoint: string,
  projectID: string,
  serviceID: string,
  deploymentID: string,
): string {
  if (!(endpoint && projectID && serviceID && deploymentID)) {
    return "";
  }
  let parsed: URL;
  try {
    parsed = new URL(endpoint);
  } catch {
    return "";
  }
  return `${parsed.origin}/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/deployments/${encodeURIComponent(deploymentID)}/deploy-logs`;
}

function writeSummary({
  title,
  project,
  resource,
  tag,
  environmentURL,
  logsURL,
  deploymentID,
  digest,
  environment,
}: {
  title: string;
  project: string;
  resource: string;
  tag: string;
  environmentURL: string;
  logsURL: string;
  deploymentID: string;
  digest: string;
  environment: string;
}): boolean {
  const summary = process.env.GITHUB_STEP_SUMMARY;
  if (!summary) {
    return false;
  }
  const lines = [`## ${title}`, ""];
  if (environmentURL) {
    lines.push(`**[Open site](${environmentURL})**`);
  }
  if (logsURL) {
    lines.push(`**[View logs in platformd](${logsURL})**`);
  }
  if (environmentURL || logsURL) {
    lines.push("");
  }
  lines.push("| | |", "| --- | --- |");
  if (project) {
    lines.push(`| Project | \`${project}\` |`);
  }
  if (resource) {
    lines.push(`| Resource | \`${resource}\` |`);
  }
  lines.push(`| Tag | \`${tag}\` |`);
  if (environment) {
    lines.push(`| GitHub environment | \`${environment}\` |`);
  }
  if (environmentURL) {
    lines.push(`| URL | ${environmentURL} |`);
  }
  if (deploymentID) {
    lines.push(`| platformd id | \`${deploymentID}\` |`);
  }
  if (digest) {
    lines.push(`| Digest | \`${digest}\` |`);
  }
  lines.push("");
  appendFileSync(summary, `${lines.join("\n")}\n`);
  return true;
}

async function createGitHubDeployment({
  token,
  tag,
  environment,
  environmentURL,
  logsURL,
}: {
  token: string;
  tag: string;
  environment: string;
  environmentURL: string;
  logsURL: string;
  deploymentID: string;
  digest: string;
}): Promise<void> {
  const repository = process.env.GITHUB_REPOSITORY;
  const ref = process.env.GITHUB_SHA;
  if (!(repository && ref)) {
    throw new Error("GITHUB_REPOSITORY and GITHUB_SHA are required");
  }
  const [owner, repo] = repository.split("/");
  if (!(owner && repo)) {
    throw new Error(`invalid GITHUB_REPOSITORY ${repository}`);
  }
  const production = tag === "latest";
  const description = production
    ? "platformd production deployment"
    : `platformd preview ${tag}`;
  const deployment = (await githubAPI(
    token,
    "POST",
    `/repos/${owner}/${repo}/deployments`,
    {
      auto_merge: false,
      description,
      environment,
      production_environment: production,
      ref,
      required_contexts: [],
      transient_environment: !production,
    },
  )) as JSONObject;
  if (!Number.isInteger(deployment.id)) {
    throw new Error("GitHub deployment response did not include an id");
  }
  await githubAPI(
    token,
    "POST",
    `/repos/${owner}/${repo}/deployments/${deployment.id as number}/statuses`,
    {
      description: environmentURL ? "Deployment is ready" : "Deployment succeeded",
      environment,
      environment_url: environmentURL || undefined,
      log_url: logsURL || undefined,
      state: "success",
    },
  );
  console.log(
    `Registered GitHub deployment ${deployment.id} on environment ${environment}` +
      (environmentURL ? ` (${environmentURL})` : "") +
      (logsURL ? `; logs ${logsURL}` : ""),
  );
}

async function resolveOpenPullNumber(
  token: string,
  owner: string,
  repo: string,
): Promise<number | undefined> {
  const eventName = process.env.GITHUB_EVENT_NAME ?? "";
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (
    (eventName === "pull_request" || eventName === "pull_request_target") &&
    eventPath
  ) {
    try {
      const event = JSON.parse(readFileSync(eventPath, "utf8")) as {
        pull_request?: { number?: unknown };
      };
      if (Number.isInteger(event.pull_request?.number)) {
        return event.pull_request?.number as number;
      }
    } catch {
      // Fall through to branch lookup.
    }
  }
  const ref = process.env.GITHUB_REF ?? "";
  if (!ref.startsWith("refs/heads/")) {
    return undefined;
  }
  const branch = ref.slice("refs/heads/".length);
  const pulls = (await githubAPI(
    token,
    "GET",
    `/repos/${owner}/${repo}/pulls?head=${encodeURIComponent(`${owner}:${branch}`)}&state=open&per_page=1`,
  )) as JSONObject[];
  if (!Array.isArray(pulls) || pulls.length === 0) {
    return undefined;
  }
  if (!Number.isInteger(pulls[0]?.number)) {
    return undefined;
  }
  return pulls[0].number as number;
}

async function commentPreviewOnPR({
  token,
  serviceID,
  previewURL,
  logsURL,
}: {
  token: string;
  serviceID: string;
  previewURL: string;
  logsURL: string;
}): Promise<void> {
  const repository = process.env.GITHUB_REPOSITORY;
  if (!repository) {
    throw new Error("GITHUB_REPOSITORY is required");
  }
  const [owner, repo] = repository.split("/");
  if (!(owner && repo)) {
    throw new Error(`invalid GITHUB_REPOSITORY ${repository}`);
  }
  const pullNumber = await resolveOpenPullNumber(token, owner, repo);
  if (pullNumber === undefined) {
    console.log("No open PR for this ref; skipping preview comment");
    return;
  }
  const marker = `<!-- platformd-preview:${serviceID} -->`;
  const lines = [marker, `### Preview \`${serviceID}\``, "", `- URL: ${previewURL}`];
  if (logsURL) {
    lines.push(`- Logs: ${logsURL}`);
  }
  const body = `${lines.join("\n")}\n`;
  const comments = await githubAPIPages(
    token,
    `/repos/${owner}/${repo}/issues/${pullNumber}/comments`,
  );
  const existing = comments.find(
    (comment) => typeof comment.body === "string" && comment.body.includes(marker),
  );
  if (existing && Number.isInteger(existing.id)) {
    await githubAPI(token, "PATCH", `/repos/${owner}/${repo}/issues/comments/${existing.id}`, {
      body,
    });
    console.log(`Updated preview comment on PR #${pullNumber}`);
    return;
  }
  await githubAPI(token, "POST", `/repos/${owner}/${repo}/issues/${pullNumber}/comments`, {
    body,
  });
  console.log(`Created preview comment on PR #${pullNumber}`);
}

async function publishDeployment({
  project,
  resource,
  tag,
  environmentURL,
  previewURL,
  logsURL,
  deploymentID,
  digest,
  serviceID,
}: {
  project: string;
  resource: string;
  tag: string;
  environmentURL: string;
  previewURL: string;
  logsURL: string;
  deploymentID: string;
  digest: string;
  serviceID: string;
}): Promise<void> {
  const token = input("github-token", process.env.GITHUB_TOKEN || "");
  const environment = input("environment", defaultEnvironmentName(resource, tag));
  const title = tag === "latest" ? "Production deployment" : "Preview deployment";

  if (token) {
    try {
      await createGitHubDeployment({
        token,
        tag,
        environment,
        environmentURL,
        logsURL,
        deploymentID,
        digest,
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      console.warn(`GitHub deployment registration skipped: ${message}`);
      if (environmentURL) {
        console.log(
          `Set environment-url output to ${environmentURL} (wire job environment.url to steps.<id>.outputs.environment-url)`,
        );
      }
    }
  } else if (environmentURL) {
    console.log(
      `Set environment-url output to ${environmentURL} (wire job environment.url to steps.<id>.outputs.environment-url)`,
    );
  }

  if (token && tag !== "latest" && previewURL) {
    try {
      await commentPreviewOnPR({
        token,
        serviceID,
        previewURL,
        logsURL,
      });
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      if (error instanceof GitHubAPIError && (error.status === 401 || error.status === 403)) {
        console.warn(
          `Preview PR comment skipped (need pull-requests: write): ${message}`,
        );
      } else {
        console.warn(`Preview PR comment skipped: ${message}`);
      }
    }
  }

  if (
    !writeSummary({
      title,
      project,
      resource,
      tag,
      environmentURL,
      logsURL,
      deploymentID,
      digest,
      environment,
    })
  ) {
    console.log(
      `${title}: id=${deploymentID || "none"} digest=${digest || "none"}` +
        (logsURL ? ` logs=${logsURL}` : ""),
    );
  }
}

function uploadEndpoint(url: string, project: string, resource: string): string {
  let parsed: URL;
  try {
    parsed = new URL(url);
  } catch {
    throw new Error("url must be a valid HTTPS origin");
  }
  if (
    parsed.protocol !== "https:" ||
    !parsed.hostname ||
    parsed.username ||
    parsed.password ||
    parsed.search ||
    parsed.hash ||
    (parsed.pathname !== "/" && parsed.pathname !== "")
  ) {
    throw new Error("url must be a valid HTTPS origin");
  }
  if (!(project && resource)) {
    throw new Error("project and resource are required");
  }
  return `${parsed.origin}/public/api/v1/projects/${encodeURIComponent(project)}/services/${encodeURIComponent(resource)}/image`;
}

async function run(): Promise<void> {
  const url = input("url");
  const project = input("project");
  const resource = input("resource");
  const archive = input("archive");
  const tag = input("tag", "latest");
  const chunkSize = parseChunkSize(input("chunk-size", String(DEFAULT_CHUNK_BYTES)));
  const concurrency = parseConcurrency(input("concurrency", String(DEFAULT_CONCURRENCY)));
  if (!(url && project && resource && archive && tag)) {
    throw new Error("url, project, resource, archive, and tag are required");
  }
  const endpoint = uploadEndpoint(url, project, resource);
  const audience = endpoint;
  const fileStat = statSync(archive);
  if (!fileStat.isFile() || fileStat.size <= 0) {
    throw new Error("archive must be a non-empty file");
  }

  const uploadID = randomUUID();
  const digest = await sha256File(archive);
  const uploadedCommitMessage = commitMessage();
  // HTTP header values cannot reliably carry arbitrary Unicode, so the
  // internal upload protocol transports the UTF-8 commit subject as base64url.
  const commitMessageHeader = uploadedCommitMessage
    ? Buffer.from(uploadedCommitMessage, "utf8").toString("base64url")
    : "";
  setOutput("upload-id", uploadID);

  const parts = splitParts(fileStat.size, chunkSize);
  const progress = new ProgressRenderer();
  progress.start(1, "uploading to platformd");

  let sentBytes = 0;
  let completedParts = 0;
  const uploadStartedAt = Date.now();
  const renderPush = (force = false) => {
    const elapsedSeconds = Math.max((Date.now() - uploadStartedAt) / 1000, 0.001);
    const percent = ((sentBytes / fileStat.size) * 100).toFixed(1);
    progress.update(
      1,
      "pushing",
      `${completedParts}/${parts.length} parts  ${formatBytes(sentBytes)} / ${formatBytes(fileStat.size)}  ${percent}%  ${formatRate(sentBytes / elapsedSeconds)}`,
      force,
    );
  };

  await mapPool(parts, concurrency, async (part) => {
    const headers = {
      "Content-Length": String(part.length),
      "Content-Type": "application/octet-stream",
      "Upload-ID": uploadID,
      "Upload-Length": String(fileStat.size),
      "Upload-Offset": String(part.offset),
      "Upload-SHA256": digest,
      "Upload-Tag": tag,
      ...(commitMessageHeader ? { "Upload-Commit-Message": commitMessageHeader } : {}),
    };
    const label = `platformd POST ${uploadRequestLabel("POST", headers)}`;
    let lastError: unknown;
    for (let attempt = 1; attempt <= PART_ATTEMPTS; attempt += 1) {
      let attemptBytes = 0;
      const source = createReadStream(archive, {
        end: part.offset + part.length - 1,
        start: part.offset,
      });
      const body = countingStream(source, (bytes) => {
        attemptBytes += bytes;
        sentBytes += bytes;
        renderPush();
      });
      try {
        await request(endpoint, audience, "POST", headers, body, PART_REQUEST_TIMEOUT_MS);
        completedParts += 1;
        renderPush(true);
        return;
      } catch (error) {
        lastError = error;
        sentBytes -= attemptBytes;
        renderPush(true);
        source.destroy();
        if (attempt === PART_ATTEMPTS || !isRetryableUploadError(error)) {
          throw error;
        }
        const delay = PART_RETRY_BASE_MS * 2 ** (attempt - 1);
        console.error(
          `${label} attempt ${attempt}/${PART_ATTEMPTS} failed; retrying in ${delay}ms: ${formatError(error)}`,
        );
        await sleep(delay);
      }
    }
    throw lastError;
  });

  progress.done(
    1,
    "pushing",
    `${parts.length}/${parts.length} parts  ${formatBytes(fileStat.size)} / ${formatBytes(fileStat.size)}  100%`,
  );

  progress.start(2, "processing image");
  let status: UploadStatus = await request(
    endpoint,
    audience,
    "GET",
    { "Upload-ID": uploadID },
    undefined,
  );
  progress.status(2, status.status || "unknown");
  while (!terminalStatuses.has(status.status ?? "")) {
    await sleep(2000);
    status = await request(endpoint, audience, "GET", { "Upload-ID": uploadID }, undefined);
    progress.status(2, status.status || "unknown");
  }
  if (status.status !== "succeeded") {
    throw new Error(
      `platformd image processing ${status.status}: ${status.errorMessage ?? status.errorCode ?? "unknown error"}`,
    );
  }
  progress.done(2, "done", status.digest || "imported");

  const deploymentID = status.deploymentId || status.previewId || "";
  const publicURL = status.url || "";
  const previewURL = tag === "latest" ? "" : publicURL;
  const logsURL = platformdLogsURL(
    endpoint,
    status.projectId || "",
    status.serviceId || "",
    deploymentID,
  );
  setOutput("deployment-id", deploymentID);
  setOutput("digest", status.digest);
  setOutput("preview-url", previewURL);
  setOutput("environment-url", publicURL);
  setOutput("logs-url", logsURL);
  await publishDeployment({
    project,
    resource,
    tag,
    environmentURL: publicURL,
    previewURL,
    logsURL,
    deploymentID,
    digest: status.digest || "",
    serviceID: resource,
  });
}

run().catch((error: unknown) => {
  const message = formatError(error);
  console.error(`image upload failed: ${message}`);
  console.error(`::error::${message.replaceAll(/[%\r\n]/gu, " ")}`);
  process.exitCode = 1;
});
