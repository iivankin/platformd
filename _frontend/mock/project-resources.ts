import type {
  ManagedPostgres,
  ManagedRedis,
  ObjectStore,
  Service,
} from "../web/api";
import {
  booleanField,
  json,
  mockError,
  numberField,
  readObject,
  stringField,
} from "./http";
import { addBackupPolicy, stringRecord, touchProject } from "./project-helpers";
import { mockDomainOutputs } from "./service-variables";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

type ResourceCollection = "object-stores" | "postgres" | "redis" | "services";
type ResourceCreator = (
  state: MockState,
  projectID: string,
  input: Record<string, unknown>
) => Response;

const initialBackupPolicy = (input: Record<string, unknown>) => {
  const value = input.backupPolicy;
  if (!(value && typeof value === "object" && !Array.isArray(value))) {
    return {};
  }
  const fields = value as Record<string, unknown>;
  return {
    cron: stringField(fields, "cron") || undefined,
    enabled: booleanField(fields, "enabled", false),
    retentionCount: numberField(fields, "retentionCount", 7),
    targetId: stringField(fields, "targetId") || undefined,
  };
};

const initialCredentials = (input: Record<string, unknown>) => {
  const value = input.credentials;
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
};

const createService: ResourceCreator = (state, projectID, input) => {
  const canvas = state.canvases[projectID];
  if (!canvas) {
    return mockError("not_found", "Project not found", 404);
  }
  const id = nextMockID(state, "service");
  const name = stringField(input, "name", "mock-resource");
  const createdAt = mockNow();
  const volumes = Array.isArray(input.volumes)
    ? input.volumes.flatMap((candidate) => {
        if (typeof candidate !== "object" || candidate === null) {
          return [];
        }
        const fields = candidate as Record<string, unknown>;
        return [
          {
            containerPath: stringField(fields, "containerPath") || undefined,
            volume: {
              createdAt,
              id: nextMockID(state, "volume"),
              name: stringField(fields, "name", "data"),
              projectId: projectID,
              serviceId: id,
            },
          },
        ];
      })
    : [];
  const service: Service = {
    cpuMillicores: 500,
    createdAt,
    enabled: booleanField(input, "enabled", true),
    environment: stringRecord(input.environment),
    healthCheck:
      typeof input.healthCheck === "object" && input.healthCheck !== null
        ? (input.healthCheck as Service["healthCheck"])
        : undefined,
    id,
    memoryMaxBytes: 536_870_912,
    name,
    projectId: projectID,
    registryCredential:
      typeof input.registryCredential === "object" &&
      input.registryCredential !== null &&
      (input.registryCredential as { password?: unknown }).password
        ? {
            ...(input.registryCredential as {
              password: string;
              username: string;
            }),
            registryHost:
              (
                input.source as { image?: { reference?: string } }
              )?.image?.reference?.split("/", 1)[0] ?? "registry.mock.local",
          }
        : undefined,
    secretReferences: [],
    source:
      typeof input.source === "object" && input.source !== null
        ? (input.source as Service["source"])
        : {
            autoUpdate: true,
            image: { reference: "docker.io/library/nginx:stable" },
            type: "public_image",
          },
    updatedAt: mockNow(),
    volumeMounts: volumes.flatMap(({ containerPath, volume }) =>
      containerPath ? [{ containerPath, volumeId: volume.id }] : []
    ),
  };
  state.services[id] = service;
  state.deployments[id] = [];
  state.domains[id] = Array.isArray(input.domains)
    ? input.domains.flatMap((candidate) => {
        if (typeof candidate !== "object" || candidate === null) {
          return [];
        }
        const fields = candidate as Record<string, unknown>;
        const hostname = stringField(fields, "hostname");
        return hostname
          ? [
              {
                createdAt,
                hostname,
                ...mockDomainOutputs(hostname),
                projectId: projectID,
                serviceId: id,
                targetPort: numberField(fields, "targetPort", 8080),
              },
            ]
          : [];
      })
    : [];
  state.listeners[id] = Array.isArray(input.listeners)
    ? input.listeners.flatMap((candidate) => {
        if (typeof candidate !== "object" || candidate === null) {
          return [];
        }
        const fields = candidate as Record<string, unknown>;
        const protocol = stringField(fields, "protocol", "tcp");
        if (protocol !== "tcp" && protocol !== "udp") {
          return [];
        }
        return [
          {
            createdAt,
            projectId: projectID,
            protocol,
            publicPort: numberField(fields, "publicPort", 3000),
            serviceId: id,
            targetPort: numberField(fields, "targetPort", 8080),
          },
        ];
      })
    : [];
  state.volumes[id] = volumes.map(({ volume }) => volume);
  state.logs[id] = { records: [], truncated: false };
  canvas.resources.push({
    enabled: service.enabled,
    id,
    internalHostname: `${name}.${canvas.project.name}.internal`,
    kind: "service",
    name,
    source: service.source,
    status: "pending",
    volumes: volumes.map(({ containerPath, volume }) => ({
      containerPath,
      id: volume.id,
      name: volume.name,
    })),
  });
  touchProject(state, projectID, "serviceCount");
  return json(service, 201);
};

const createRedis: ResourceCreator = (state, projectID, input) => {
  const canvas = state.canvases[projectID];
  if (!canvas) {
    return mockError("not_found", "Project not found", 404);
  }
  const id = nextMockID(state, "redis");
  const name = stringField(input, "name", "mock-resource");
  const credentials = initialCredentials(input);
  const resource: ManagedRedis = {
    backupEnabled: false,
    backupRetentionCount: 5,
    cpuMillicores: numberField(input, "cpuMillicores", 250),
    createdAt: mockNow(),
    hostname: `${name}.${canvas.project.name}.internal`,
    id,
    imageDigest: "sha256:redis-mock",
    imageTag: stringField(input, "imageTag", "8.2"),
    memoryBytes: numberField(input, "memoryBytes", 268_435_456),
    name,
    password: stringField(credentials, "password", "mock-only-redis-password"),
    port: 6379,
    projectId: projectID,
    updatedAt: mockNow(),
  };
  state.redis[id] = resource;
  state.runtimeDeployments[id] = [];
  canvas.resources.push({
    enabled: true,
    id,
    imageDigest: resource.imageDigest,
    imageReference: `redis:${resource.imageTag}`,
    internalHostname: resource.hostname,
    kind: "redis",
    name,
    status: "pending",
    volumes: [],
  });
  addBackupPolicy(state, "redis", id, initialBackupPolicy(input));
  touchProject(state, projectID, "redisCount");
  return json(resource, 201);
};

const createPostgres: ResourceCreator = (state, projectID, input) => {
  const canvas = state.canvases[projectID];
  if (!canvas) {
    return mockError("not_found", "Project not found", 404);
  }
  const id = nextMockID(state, "postgres");
  const name = stringField(input, "name", "mock-resource");
  const credentials = initialCredentials(input);
  const databaseName = stringField(
    credentials,
    "databaseName",
    name.replaceAll("-", "_")
  );
  const resource: ManagedPostgres = {
    backupEnabled: false,
    backupRetentionCount: 5,
    cpuMillicores: numberField(input, "cpuMillicores", 500),
    createdAt: mockNow(),
    databaseName,
    hostname: `${name}.${canvas.project.name}.internal`,
    id,
    imageDigest: "sha256:postgres-mock",
    imageTag: stringField(input, "imageTag", "17.5"),
    memoryBytes: numberField(input, "memoryBytes", 1_073_741_824),
    name,
    ownerPassword: stringField(
      credentials,
      "ownerPassword",
      "mock-only-postgres-password"
    ),
    ownerUsername: stringField(
      credentials,
      "ownerUsername",
      `${databaseName}_owner`
    ),
    port: 5432,
    projectId: projectID,
    updatedAt: mockNow(),
  };
  state.postgres[id] = resource;
  state.runtimeDeployments[id] = [];
  canvas.resources.push({
    enabled: true,
    id,
    imageDigest: resource.imageDigest,
    imageReference: `postgres:${resource.imageTag}`,
    internalHostname: resource.hostname,
    kind: "postgres",
    name,
    status: "pending",
    volumes: [],
  });
  addBackupPolicy(state, "postgres", id, initialBackupPolicy(input));
  touchProject(state, projectID, "postgresCount");
  return json(resource, 201);
};

const createObjectStore: ResourceCreator = (state, projectID, input) => {
  const canvas = state.canvases[projectID];
  if (!canvas) {
    return mockError("not_found", "Project not found", 404);
  }
  const id = nextMockID(state, "object-store");
  const name = stringField(input, "name", "mock-resource");
  const credentials = initialCredentials(input);
  const resource: ObjectStore = {
    accessKey: stringField(credentials, "accessKey", "MOCK_ONLY_ACCESS_KEY"),
    backupEnabled: false,
    backupRetentionCount: 5,
    bucketName: stringField(input, "bucketName", `${name}-bucket`),
    corsOrigins: Array.isArray(input.corsOrigins)
      ? input.corsOrigins.filter(
          (origin): origin is string => typeof origin === "string"
        )
      : [],
    createdAt: mockNow(),
    credentialPermission: "read_write",
    id,
    internalHostname: `${name}.${canvas.project.name}.internal`,
    name,
    projectId: projectID,
    publicHostname: stringField(input, "publicHostname") || undefined,
    region: "us-east-1",
    secret: stringField(credentials, "secret", "mock-only-object-secret"),
    updatedAt: mockNow(),
  };
  state.objectStores[id] = resource;
  state.objectMetadata[id] = [];
  canvas.resources.push({
    bucketName: resource.bucketName,
    enabled: true,
    id,
    internalHostname: resource.internalHostname,
    kind: "object_store",
    name,
    status: "running",
    volumes: [],
  });
  addBackupPolicy(state, "object_store", id, initialBackupPolicy(input));
  touchProject(state, projectID, "objectStoreCount");
  return json(resource, 201);
};

const resourceCreators: Record<ResourceCollection, ResourceCreator> = {
  "object-stores": createObjectStore,
  postgres: createPostgres,
  redis: createRedis,
  services: createService,
};

const isResourceCollection = (value: string): value is ResourceCollection =>
  Object.hasOwn(resourceCreators, value);

export const handleResourceCreation = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, projectID, collection, ...rest] = segments;
  if (
    request.method !== "POST" ||
    root !== "projects" ||
    !projectID ||
    !collection ||
    rest.length > 0 ||
    !isResourceCollection(collection)
  ) {
    return undefined;
  }
  const input = await readObject(request);
  return resourceCreators[collection](state, projectID, input);
};
