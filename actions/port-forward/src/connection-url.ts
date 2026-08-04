const PROTOCOLS = {
  postgres: new Set(["postgres:", "postgresql:"]),
  redis: new Set(["redis:", "rediss:"]),
} as const;

export type ResourceKind = "service" | "postgres" | "redis" | "object_store";

export type ConnectionConfig = {
  connectionUrl: string;
  connectionEnv: string;
  localPort: number;
  resourceKind: ResourceKind;
};

export type ForwardedConnection = {
  environment: string;
  url: string;
};

export function forwardedConnection(config: ConnectionConfig): ForwardedConnection | null {
  if (!config.connectionUrl) {
    return null;
  }
  let url: URL;
  if (config.resourceKind !== "postgres" && config.resourceKind !== "redis") {
    throw new Error("connection-url is supported only for postgres and redis");
  }
  try {
    url = new URL(config.connectionUrl);
  } catch {
    throw new Error("connection-url must be a valid URL");
  }
  if (!url.hostname || !PROTOCOLS[config.resourceKind]?.has(url.protocol)) {
    const expected =
      config.resourceKind === "postgres" ? "postgres:// or postgresql://" : "redis:// or rediss://";
    throw new Error(`connection-url for ${config.resourceKind} must use ${expected}`);
  }
  url.hostname = "127.0.0.1";
  url.port = String(config.localPort);
  const environment =
    config.connectionEnv || (config.resourceKind === "postgres" ? "POSTGRES_URL" : "REDIS_URL");
  return { environment, url: url.toString() };
}
