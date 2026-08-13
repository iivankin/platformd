const STABLE_VERSION = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

export type ForwardTarget = {
  os: "linux" | "darwin";
  arch: "amd64" | "arm64";
};

export type ActionConfig = {
  baseUrl: string;
  token: string;
  project: string;
  resource: string;
  endpoint: string;
  port: number;
  localPort: number;
  expiresInSeconds: number;
  version: string;
  binaryPath: string;
  connectionUrl: string;
  connectionEnv: string;
  target: ForwardTarget;
};

export type GetInput = (name: string) => string;

export function readConfig(
  getInput: GetInput,
  platform: NodeJS.Platform = process.platform,
  architecture: NodeJS.Architecture = process.arch,
): ActionConfig {
  const url = requiredInput(getInput, "url");
  const token = getInput("token").trim();
  const project = requiredInput(getInput, "project");
  const resource = requiredInput(getInput, "resource");
  const endpoint = getInput("endpoint").trim();
  if (endpoint && endpoint !== "errors") {
    throw new Error("endpoint must be errors when set");
  }
  const portInput = getInput("port").trim();
  const port = endpoint === "errors"
    ? errorsPort(portInput)
    : parseInteger(portInput || requiredInput(getInput, "port"), "port", 1, 65535);
  const localPortInput = getInput("local-port").trim();
  const localPort = localPortInput
    ? parseInteger(localPortInput, "local-port", 1, 65535)
    : port;
  const expiresInSeconds = parseInteger(
    getInput("expires-in-seconds").trim() || "3600",
    "expires-in-seconds",
    60,
    28800,
  );

  const connectionUrl = getInput("connection-url").trim();
  const connectionEnv = validateConnectionEnvironment(
    connectionUrl,
    getInput("connection-env").trim(),
  );

  return {
    baseUrl: normalizeBaseUrl(url),
    token,
    project,
    resource,
    endpoint,
    port,
    localPort,
    expiresInSeconds,
    version: normalizeVersion(getInput("platformd-version").trim() || "latest"),
    binaryPath: getInput("binary-path").trim(),
    connectionUrl,
    connectionEnv,
    target: resolveTarget(platform, architecture),
  };
}

function errorsPort(value: string): number {
  if (value) {
    throw new Error("port must be omitted when endpoint is errors");
  }
  return 9001;
}

function requiredInput(getInput: GetInput, name: string): string {
  const value = getInput(name).trim();
  if (!value) {
    throw new Error(`${name} is required`);
  }
  return value;
}

export function normalizeBaseUrl(value: string): string {
  let parsed: URL;
  try {
    parsed = new URL(value);
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
  return parsed.origin;
}

export function normalizeVersion(value: string): string {
  if (value === "latest") {
    return value;
  }
  const normalized = value.startsWith("v") ? value.slice(1) : value;
  if (!STABLE_VERSION.test(normalized)) {
    throw new Error("platformd-version must be latest or an exact stable SemVer");
  }
  return normalized;
}

export function parseInteger(
  value: string,
  name: string,
  minimum: number,
  maximum: number,
): number {
  if (!/^\d+$/.test(value)) {
    throw new Error(`${name} must be an integer from ${minimum} to ${maximum}`);
  }
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < minimum || parsed > maximum) {
    throw new Error(`${name} must be an integer from ${minimum} to ${maximum}`);
  }
  return parsed;
}

export function resolveTarget(
  platform: NodeJS.Platform | string,
  architecture: NodeJS.Architecture | string,
): ForwardTarget {
  const os = platform === "linux" ? "linux" : platform === "darwin" ? "darwin" : "";
  const arch = architecture === "x64" ? "amd64" : architecture === "arm64" ? "arm64" : "";
  if (!os || !arch) {
    throw new Error(`unsupported runner platform: ${platform}/${architecture}`);
  }
  return { os, arch };
}

export function validateConnectionEnvironment(
  connectionUrl: string,
  requestedEnvironment: string,
): string {
  if (!connectionUrl) {
    if (requestedEnvironment) {
      throw new Error("connection-env requires connection-url");
    }
    return "";
  }
  if (requestedEnvironment && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(requestedEnvironment)) {
    throw new Error("connection-env must be a valid environment variable name");
  }
  return requestedEnvironment;
}
