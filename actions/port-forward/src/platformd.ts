import { createHash } from "node:crypto";
import { chmod, mkdir, mkdtemp, rm, stat, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import type { ActionConfig, ForwardTarget } from "./config.js";
import type { ResourceKind } from "./connection-url.js";

const FORWARD_ENDPOINT = "/public/api/v1/port-forward";
const RELEASES_URL = "https://github.com/iivankin/platformd/releases";
const SUPPORTED_RESOURCE_KINDS = new Set<ResourceKind>([
  "service",
  "postgres",
  "redis",
  "object_store",
]);

export type PortForwardGrant = {
  ticket: string;
  websocketUrl: string;
  expiresAt: string;
  resourceKind: ResourceKind;
  endpointHost: string;
};

export type PreparedBinary = {
  binaryPath: string;
  workDir: string;
};

export type FetchLike = typeof fetch;

export type BinaryOptions = {
  temporaryRoot?: string;
  fetchImplementation?: FetchLike;
};

type PortForwardConfig = Pick<
  ActionConfig,
  "baseUrl" | "token" | "project" | "resource" | "port" | "localPort" | "expiresInSeconds"
  | "endpoint"
>;

type DownloadConfig = {
  version: string;
  target: ForwardTarget;
};

type LocalBinaryConfig = {
  binaryPath: string;
};

type PrepareConfig = LocalBinaryConfig & DownloadConfig;

type PortForwardResponse = {
  ticket?: unknown;
  expiresAt?: unknown;
  project?: unknown;
  resource?: unknown;
  resourceKind?: unknown;
  endpoint?: unknown;
  endpointHost?: unknown;
  instructions?: { websocketUrl?: unknown };
  error?: { message?: unknown };
};

async function oidcToken(audience: string, fetchImplementation: FetchLike): Promise<string> {
  const requestURL = process.env.ACTIONS_ID_TOKEN_REQUEST_URL;
  const requestToken = process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  if (!(requestURL && requestToken)) {
    throw new Error(
      "GitHub OIDC is unavailable. Add `permissions: id-token: write` or pass token.",
    );
  }
  const url = new URL(requestURL);
  url.searchParams.set("audience", audience);
  const response = await fetchImplementation(url, {
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

export function portForwardEndpoint(config: Pick<PortForwardConfig, "baseUrl" | "project" | "resource">): string {
  const path = [
    "public",
    "api",
    "v1",
    "projects",
    encodeURIComponent(config.project),
    "resources",
    encodeURIComponent(config.resource),
    "port-forwards",
  ].join("/");
  return `${config.baseUrl}/${path}`;
}

export async function createPortForward(
  config: PortForwardConfig,
  fetchImplementation: FetchLike = fetch,
): Promise<PortForwardGrant> {
  const endpoint = portForwardEndpoint(config);
  const authorization = config.token
    ? `Bearer ${config.token}`
    : `Bearer ${await oidcToken(endpoint, fetchImplementation)}`;
  const response = await fetchImplementation(endpoint, {
    method: "POST",
    headers: {
      accept: "application/json",
      authorization,
      "content-type": "application/json",
    },
    body: JSON.stringify({
      port: config.endpoint ? undefined : config.port,
      endpoint: config.endpoint || undefined,
      localPort: config.localPort,
      expiresInSeconds: config.expiresInSeconds,
    }),
  });
  const body = (await parseJSONResponse(response)) as PortForwardResponse;
  if (!response.ok) {
    const detail = body?.error?.message;
    throw new Error(
      `platformd rejected the port forward request with HTTP ${response.status}${
        typeof detail === "string" && detail ? `: ${detail}` : ""
      }`,
    );
  }

  const ticket = body?.ticket;
  const websocketUrl = body?.instructions?.websocketUrl;
  const expiresAt = body?.expiresAt;
  const resourceKind = body?.resourceKind;
  const responseEndpoint = body?.endpoint;
  const endpointHost = body?.endpointHost;
  if (
    typeof ticket !== "string" ||
    !ticket.startsWith("pft_") ||
    !validWebSocketUrl(websocketUrl) ||
    typeof expiresAt !== "string" ||
    !expiresAt ||
    body?.project !== config.project ||
    body?.resource !== config.resource ||
    typeof resourceKind !== "string" ||
    !SUPPORTED_RESOURCE_KINDS.has(resourceKind as ResourceKind) ||
    (config.endpoint === "errors" &&
      (resourceKind !== "service" || responseEndpoint !== "errors" || typeof endpointHost !== "string" || !endpointHost)) ||
    (!config.endpoint && (responseEndpoint !== undefined || endpointHost !== undefined))
  ) {
    throw new Error("platformd returned an invalid port forward response");
  }
  return {
    ticket,
    websocketUrl: websocketUrl as string,
    expiresAt,
    resourceKind: resourceKind as ResourceKind,
    endpointHost: typeof endpointHost === "string" ? endpointHost : "",
  };
}

export async function prepareForwardBinary(
  config: PrepareConfig,
  options: BinaryOptions = {},
): Promise<PreparedBinary> {
  if (config.binaryPath) {
    return useLocalForward(config.binaryPath, options);
  }
  return downloadForward(config, options);
}

export async function useLocalForward(
  binaryPath: string,
  options: BinaryOptions = {},
): Promise<PreparedBinary> {
  const temporaryRoot = options.temporaryRoot || process.env.RUNNER_TEMP || tmpdir();
  await mkdir(temporaryRoot, { recursive: true });
  const resolvedPath = resolve(binaryPath);
  let info;
  try {
    info = await stat(resolvedPath);
  } catch {
    throw new Error(`forward binary not found: ${resolvedPath}`);
  }
  if (!info.isFile()) {
    throw new Error(`forward binary is not a file: ${resolvedPath}`);
  }
  const workDir = await mkdtemp(join(temporaryRoot, "platformd-port-forward-"));
  return { binaryPath: resolvedPath, workDir };
}

export async function downloadForward(
  config: DownloadConfig,
  options: BinaryOptions = {},
): Promise<PreparedBinary> {
  const fetchImplementation = options.fetchImplementation || fetch;
  const temporaryRoot = options.temporaryRoot || process.env.RUNNER_TEMP || tmpdir();
  await mkdir(temporaryRoot, { recursive: true });
  const workDir = await mkdtemp(join(temporaryRoot, "platformd-port-forward-"));
  const asset = `platformd-forward-${config.target.os}-${config.target.arch}`;
  const releaseBase =
    config.version === "latest"
      ? `${RELEASES_URL}/latest/download`
      : `${RELEASES_URL}/download/v${encodeURIComponent(config.version)}`;

  try {
    const [binaryResponse, checksumsResponse] = await Promise.all([
      fetchImplementation(`${releaseBase}/${asset}`),
      fetchImplementation(`${releaseBase}/SHA256SUMS`),
    ]);
    if (!binaryResponse.ok) {
      throw new Error(`failed to download ${asset}: HTTP ${binaryResponse.status}`);
    }
    if (!checksumsResponse.ok) {
      throw new Error(`failed to download SHA256SUMS: HTTP ${checksumsResponse.status}`);
    }

    const binary = Buffer.from(await binaryResponse.arrayBuffer());
    const checksums = await checksumsResponse.text();
    verifyChecksum(asset, binary, checksums);

    const binaryPath = join(workDir, "platformd-forward");
    await writeFile(binaryPath, binary, { mode: 0o700 });
    await chmod(binaryPath, 0o700);
    return { binaryPath, workDir };
  } catch (error) {
    await rm(workDir, { force: true, recursive: true });
    throw error;
  }
}

export function verifyChecksum(asset: string, binary: Buffer, manifest: string): void {
  const expected = manifest
    .split(/\r?\n/)
    .map((line) => line.match(/^([a-fA-F0-9]{64})[ \t]+\*?(.+)$/))
    .find((match) => match && match[2].trim() === asset)?.[1];
  if (!expected) {
    throw new Error(`SHA256SUMS does not contain ${asset}`);
  }
  const actual = createHash("sha256").update(binary).digest("hex");
  if (actual !== expected.toLowerCase()) {
    throw new Error(`checksum verification failed for ${asset}`);
  }
}

export function validWebSocketUrl(value: unknown): value is string {
  if (typeof value !== "string") {
    return false;
  }
  try {
    const parsed = new URL(value);
    return (
      parsed.protocol === "wss:" &&
      Boolean(parsed.hostname) &&
      !parsed.username &&
      !parsed.password &&
      parsed.pathname === FORWARD_ENDPOINT &&
      !parsed.search &&
      !parsed.hash
    );
  } catch {
    return false;
  }
}

async function parseJSONResponse(response: Response): Promise<unknown> {
  let text: string;
  try {
    text = await response.text();
  } catch {
    throw new Error(`unable to read platformd response with HTTP ${response.status}`);
  }
  try {
    return JSON.parse(text);
  } catch {
    throw new Error(`platformd returned non-JSON with HTTP ${response.status}`);
  }
}
