import { createHash, randomUUID } from "node:crypto";
import { appendFileSync, createReadStream, readFileSync, statSync } from "node:fs";
import type { Readable } from "node:stream";

const terminalStatuses = new Set(["failed", "succeeded", "superseded"]);

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

async function oidcToken(audience: string): Promise<string> {
  const requestURL = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
  const requestToken = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  if (!(requestURL && requestToken)) {
    throw new Error(
      "GitHub OIDC is unavailable. Add `permissions: id-token: write` to the workflow.",
    );
  }
  const url = new URL(requestURL);
  url.searchParams.set("audience", audience);
  const response = await fetch(url, {
    headers: { Authorization: `Bearer ${requestToken}` },
  });
  if (!response.ok) {
    throw new Error(`GitHub OIDC token request failed with ${response.status}`);
  }
  const body = (await response.json()) as { value?: unknown };
  if (typeof body.value !== "string" || !body.value) {
    throw new Error("GitHub OIDC token response did not contain a token");
  }
  return body.value;
}

async function responseBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (!text) {
    return {};
  }
  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new Error(`platformd returned invalid JSON with ${response.status}`);
  }
}

async function request(
  endpoint: string,
  audience: string,
  method: string,
  headers: Record<string, string>,
  body: Readable | undefined,
): Promise<UploadStatus> {
  const token = await oidcToken(audience);
  const response = await fetch(endpoint, {
    body,
    duplex: body ? "half" : undefined,
    headers: {
      Accept: "application/json",
      Authorization: `Bearer ${token}`,
      ...headers,
    },
    method,
  } as RequestInit);
  const payload = await responseBody(response);
  if (!response.ok) {
    const error =
      payload && typeof payload === "object"
        ? (payload as { error?: { message?: unknown } }).error
        : undefined;
    const detail = typeof error?.message === "string" ? error.message : `HTTP ${response.status}`;
    throw new Error(`platformd image upload failed: ${detail}`);
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
  const response = await fetch(`https://api.github.com${path}`, {
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
  const chunkSize = Number(input("chunk-size", "8388608"));
  if (!(url && project && resource && archive && tag)) {
    throw new Error("url, project, resource, archive, and tag are required");
  }
  if (!Number.isSafeInteger(chunkSize) || chunkSize <= 0) {
    throw new Error("chunk-size must be a positive integer");
  }
  const endpoint = uploadEndpoint(url, project, resource);
  const audience = endpoint;
  const fileStat = statSync(archive);
  if (!fileStat.isFile() || fileStat.size <= 0) {
    throw new Error("archive must be a non-empty file");
  }

  const uploadID = randomUUID();
  const digest = await sha256File(archive);
  setOutput("upload-id", uploadID);
  console.log(`Uploading ${fileStat.size} bytes as ${tag} in ${chunkSize}-byte chunks`);

  let offset = 0;
  let status: UploadStatus = {};
  while (offset < fileStat.size) {
    const length = Math.min(chunkSize, fileStat.size - offset);
    const stream = createReadStream(archive, {
      end: offset + length - 1,
      start: offset,
    });
    status = await request(
      endpoint,
      audience,
      "POST",
      {
        "Content-Length": String(length),
        "Content-Type": "application/octet-stream",
        "Upload-ID": uploadID,
        "Upload-Length": String(fileStat.size),
        "Upload-Offset": String(offset),
        "Upload-SHA256": digest,
        "Upload-Tag": tag,
      },
      stream,
    );
    if (!Number.isSafeInteger(status.offset) || (status.offset ?? 0) <= offset) {
      throw new Error("platformd did not advance the upload offset");
    }
    offset = status.offset as number;
    console.log(`Uploaded ${offset}/${fileStat.size} bytes`);
  }

  while (!terminalStatuses.has(status.status ?? "")) {
    await sleep(2000);
    status = await request(endpoint, audience, "GET", { "Upload-ID": uploadID }, undefined);
    console.log(`platformd image status: ${status.status}`);
  }
  if (status.status !== "succeeded") {
    throw new Error(
      `platformd image processing ${status.status}: ${status.errorMessage ?? status.errorCode ?? "unknown error"}`,
    );
  }
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
  const message = error instanceof Error ? error.message : String(error);
  console.error(`::error::${message.replaceAll(/[%\r\n]/gu, " ")}`);
  process.exitCode = 1;
});
