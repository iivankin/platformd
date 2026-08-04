import { createHash, randomUUID } from "node:crypto";
import { appendFileSync, createReadStream, statSync } from "node:fs";
import type { Readable } from "node:stream";

const terminalStatuses = new Set(["failed", "succeeded", "superseded"]);

type JSONObject = Record<string, unknown>;

type UploadStatus = {
  offset?: number;
  status?: string;
  errorMessage?: string;
  errorCode?: string;
  deploymentId?: string;
  previewId?: string;
  previewUrl?: string;
  digest?: string;
};

function input(name: string, fallback = ""): string {
  const key = `INPUT_${name.replaceAll(" ", "_").toUpperCase()}`;
  return (process.env[key] ?? fallback).trim();
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

async function responseBody(response: Response): Promise<JSONObject> {
  const text = await response.text();
  if (!text) {
    return {};
  }
  try {
    return JSON.parse(text) as JSONObject;
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
    const error = payload.error as { message?: unknown } | undefined;
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

async function githubAPI(
  token: string,
  method: string,
  path: string,
  body?: JSONObject,
): Promise<JSONObject> {
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
      typeof payload.message === "string" ? payload.message : `HTTP ${response.status}`;
    throw new Error(`GitHub API ${path} failed: ${detail}`);
  }
  return payload;
}

function defaultEnvironmentName(tag: string): string {
  if (tag === "latest") {
    return "production";
  }
  const safe = tag.replaceAll(/[^A-Za-z0-9._-]/gu, "-").replaceAll(/-+/gu, "-");
  return `preview-${safe}`.slice(0, 255);
}

function platformdLogsURL(endpoint: string, deploymentID: string): string {
  if (!(endpoint && deploymentID)) {
    return "";
  }
  let parsed: URL;
  try {
    parsed = new URL(endpoint);
  } catch {
    return "";
  }
  const match = parsed.pathname.match(
    /^\/public\/api\/v1\/projects\/([^/]+)\/services\/([^/]+)\/image\/?$/u,
  );
  if (!match) {
    return "";
  }
  const projectID = decodeURIComponent(match[1]);
  const serviceID = decodeURIComponent(match[2]);
  return `${parsed.origin}/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/deployments/${encodeURIComponent(deploymentID)}/deploy-logs`;
}

function writeSummary({
  title,
  environmentURL,
  logsURL,
  deploymentID,
  digest,
  environment,
}: {
  title: string;
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
  const lines = [`### ${title}`, ""];
  if (environmentURL) {
    lines.push(`[Open site](${environmentURL})`);
  }
  if (logsURL) {
    lines.push(`[View logs in platformd](${logsURL})`);
  }
  if (environmentURL || logsURL) {
    lines.push("");
  }
  if (environment) {
    lines.push(`- environment: \`${environment}\``);
  }
  if (deploymentID) {
    lines.push(`- platformd id: \`${deploymentID}\``);
  }
  if (digest) {
    lines.push(`- digest: \`${digest}\``);
  }
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
  const deployment = await githubAPI(token, "POST", `/repos/${owner}/${repo}/deployments`, {
    auto_merge: false,
    description,
    environment,
    production_environment: production,
    ref,
    required_contexts: [],
    transient_environment: !production,
  });
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

async function publishDeployment({
  tag,
  environmentURL,
  logsURL,
  deploymentID,
  digest,
}: {
  tag: string;
  environmentURL: string;
  logsURL: string;
  deploymentID: string;
  digest: string;
}): Promise<void> {
  const token = input("github-token", process.env.GITHUB_TOKEN || "");
  const environment = input("environment", defaultEnvironmentName(tag));
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

  if (
    !writeSummary({
      title,
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
  const environmentURL = input("environment-url") || status.previewUrl || "";
  const logsURL = platformdLogsURL(endpoint, deploymentID);
  setOutput("deployment-id", deploymentID);
  setOutput("digest", status.digest);
  setOutput("preview-url", status.previewUrl);
  setOutput("environment-url", environmentURL);
  setOutput("logs-url", logsURL);
  await publishDeployment({
    tag,
    environmentURL,
    logsURL,
    deploymentID,
    digest: status.digest || "",
  });
}

run().catch((error: unknown) => {
  const message = error instanceof Error ? error.message : String(error);
  console.error(`::error::${message.replaceAll(/[%\r\n]/gu, " ")}`);
  process.exitCode = 1;
});
