import type { ProjectCanvas } from "../web/api";
import type { MockState } from "./state";

const expressionPattern =
  /\$\{\{\s*(?<resource>[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\.(?<output>[A-Za-z_][A-Za-z0-9_]*)\s*\}\}/gu;
const expressionOpening = `${String.fromCodePoint(36)}{{`;

const connectionURL = (
  scheme: string,
  username: string,
  password: string,
  hostname: string,
  port: number,
  path: string
) =>
  `${scheme}://${encodeURIComponent(username)}:${encodeURIComponent(password)}@${hostname}:${port}/${encodeURIComponent(path)}`;

class MockEnvironmentResolution {
  private readonly cache = new Map<string, string>();
  private readonly resources: Map<string, ProjectCanvas["resources"][number]>;
  private readonly resolving = new Set<string>();
  private readonly serviceID: string;
  private readonly state: MockState;

  constructor(state: MockState, serviceID: string) {
    this.state = state;
    this.serviceID = serviceID;
    const service = state.services[serviceID];
    if (!service) {
      throw new Error("Service not found");
    }
    this.resources = new Map(
      (state.canvases[service.projectId]?.resources ?? []).map((resource) => [
        resource.name,
        resource,
      ])
    );
  }

  resolve(): Record<string, string> {
    const service = this.state.services[this.serviceID];
    if (!service) {
      throw new Error("Service not found");
    }
    return Object.fromEntries(
      Object.keys(service.environment).map((name) => [
        name,
        this.serviceVariable(service.id, name),
      ])
    );
  }

  private expand(value: string): string {
    let cursor = 0;
    let result = "";
    for (const match of value.matchAll(expressionPattern)) {
      const resource = match.groups?.resource;
      const output = match.groups?.output;
      if (match.index === undefined || !resource || !output) {
        throw new Error("Invalid variable reference");
      }
      const literal = value.slice(cursor, match.index);
      if (literal.includes(expressionOpening)) {
        throw new Error("Invalid variable reference");
      }
      result += literal + this.reference(resource, output);
      cursor = match.index + match[0].length;
    }
    const remainder = value.slice(cursor);
    if (remainder.includes(expressionOpening)) {
      throw new Error("Invalid variable reference");
    }
    return result + remainder;
  }

  private reference(resourceName: string, output: string): string {
    const resource = this.resources.get(resourceName);
    if (!resource) {
      throw new Error(`Resource ${resourceName} does not exist`);
    }
    if (resource.kind === "service") {
      return this.serviceOutput(resource, output);
    }
    if (resource.kind === "postgres") {
      return this.postgresOutput(resource, output);
    }
    if (resource.kind === "redis") {
      return this.redisOutput(resource, output);
    }
    return this.objectStoreOutput(resource, output);
  }

  private serviceVariable(sourceID: string, output: string): string {
    const key = `${sourceID}.${output}`;
    const cached = this.cache.get(key);
    if (cached !== undefined) {
      return cached;
    }
    if (this.resolving.has(key)) {
      throw new Error(`Variable reference cycle at ${key}`);
    }
    const source = this.state.services[sourceID];
    const raw = source?.environment[output];
    if (!(source && raw !== undefined)) {
      throw new Error(`${source?.name ?? sourceID} does not export ${output}`);
    }
    this.resolving.add(key);
    const value = this.expand(raw);
    this.resolving.delete(key);
    this.cache.set(key, value);
    return value;
  }

  private serviceOutput(
    resource: ProjectCanvas["resources"][number],
    output: string
  ): string {
    const source = this.state.services[resource.id];
    if (source && output in source.environment) {
      return this.serviceVariable(resource.id, output);
    }
    const domain = (this.state.domains[resource.id] ?? []).find(
      (candidate) =>
        candidate.publicOutputName === output ||
        candidate.internalOutputName === output
    );
    if (!(source && domain)) {
      throw new Error(`${resource.name} does not export ${output}`);
    }
    if (domain.publicOutputName === output) {
      return `https://${domain.hostname}`;
    }
    const project = this.state.projects.find(
      (candidate) => candidate.id === source.projectId
    );
    return `http://${source.name}.${project?.name ?? "project"}.internal:${domain.targetPort}`;
  }

  private postgresOutput(
    resource: ProjectCanvas["resources"][number],
    output: string
  ): string {
    const postgres = this.state.postgres[resource.id];
    if (!postgres) {
      throw new Error(`PostgreSQL ${resource.name} does not exist`);
    }
    const url = connectionURL(
      "postgresql",
      postgres.ownerUsername,
      postgres.ownerPassword,
      postgres.hostname,
      postgres.port,
      postgres.databaseName
    );
    const outputs: Record<string, string> = {
      DATABASE_URL: url,
      PGDATABASE: postgres.databaseName,
      PGHOST: postgres.hostname,
      PGPASSWORD: postgres.ownerPassword,
      PGPORT: String(postgres.port),
      PGUSER: postgres.ownerUsername,
      POSTGRES_URL: url,
    };
    const value = outputs[output];
    if (value === undefined) {
      throw new Error(`PostgreSQL ${resource.name} does not export ${output}`);
    }
    return value;
  }

  private redisOutput(
    resource: ProjectCanvas["resources"][number],
    output: string
  ): string {
    const redis = this.state.redis[resource.id];
    if (!redis) {
      throw new Error(`Redis ${resource.name} does not exist`);
    }
    const outputs: Record<string, string> = {
      REDISHOST: redis.hostname,
      REDISPASSWORD: redis.password,
      REDISPORT: String(redis.port),
      REDIS_URL: connectionURL(
        "redis",
        "",
        redis.password,
        redis.hostname,
        redis.port,
        "0"
      ),
    };
    const value = outputs[output];
    if (value === undefined) {
      throw new Error(`Redis ${resource.name} does not export ${output}`);
    }
    return value;
  }

  private objectStoreOutput(
    resource: ProjectCanvas["resources"][number],
    output: string
  ): string {
    const objectStore = this.state.objectStores[resource.id];
    if (!objectStore) {
      throw new Error(`Object store ${resource.name} does not exist`);
    }
    const outputs: Record<string, string> = {
      S3_ACCESS_KEY_ID: objectStore.accessKey,
      S3_BUCKET: objectStore.bucketName,
      S3_ENDPOINT: `http://${objectStore.internalHostname}:9000`,
      S3_REGION: objectStore.region,
      S3_SECRET_ACCESS_KEY: objectStore.secret,
    };
    const value = outputs[output];
    if (value === undefined) {
      throw new Error(
        `Object store ${resource.name} does not export ${output}`
      );
    }
    return value;
  }
}

export const resolveMockEnvironment = (
  state: MockState,
  serviceID: string
): Record<string, string> =>
  new MockEnvironmentResolution(state, serviceID).resolve();

export const referencedResourceNames = (value: string): string[] =>
  [...value.matchAll(expressionPattern)].flatMap((match) =>
    match.groups?.resource ? [match.groups.resource] : []
  );

export const mockDomainOutputs = (hostname: string) => {
  const labels = hostname.toLowerCase().split(".");
  const prefix = labels.length > 2 ? labels.slice(0, -2).join(".") : hostname;
  const normalized = prefix.replaceAll(/[^a-z0-9]+/gu, "_").toUpperCase();
  return {
    internalOutputName: `${normalized}_URL_INTERNAL`,
    publicOutputName: `${normalized}_URL`,
  };
};
