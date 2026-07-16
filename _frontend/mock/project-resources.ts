import type {
  ManagedPostgres,
  ManagedRedis,
  ObjectStore,
  Service,
} from "../web/api";
import { json, mockError, numberField, readObject, stringField } from "./http";
import { addBackupPolicy, stringRecord, touchProject } from "./project-helpers";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

type ResourceCollection = "object-stores" | "postgres" | "redis" | "services";
type ResourceCreator = (
  state: MockState,
  projectID: string,
  input: Record<string, unknown>
) => Response;

const createService: ResourceCreator = (state, projectID, input) => {
  const canvas = state.canvases[projectID];
  if (!canvas) {
    return mockError("not_found", "Project not found", 404);
  }
  const id = nextMockID(state, "service");
  const name = stringField(input, "name", "mock-resource");
  const service: Service = {
    cpuMillicores: 500,
    createdAt: mockNow(),
    enabled: true,
    environment: stringRecord(input.environment),
    healthCheck:
      typeof input.healthCheck === "object" && input.healthCheck !== null
        ? (input.healthCheck as Service["healthCheck"])
        : undefined,
    id,
    imageReference: stringField(input, "imageReference", "nginx:stable"),
    memoryMaxBytes: 536_870_912,
    name,
    projectId: projectID,
    secretReferences: [],
    updatedAt: mockNow(),
    volumeMounts: [],
  };
  state.services[id] = service;
  state.deployments[id] = [];
  state.domains[id] = [];
  state.volumes[id] = [];
  state.logs[id] = { records: [], truncated: false };
  canvas.resources.push({
    enabled: true,
    id,
    imageReference: service.imageReference,
    internalHostname: `${name}.${canvas.project.name}.internal`,
    kind: "service",
    name,
    status: "pending",
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
    password: "mock-only-redis-password",
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
  });
  addBackupPolicy(state, "redis", id);
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
  const databaseName = name.replaceAll("-", "_");
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
    ownerPassword: "mock-only-postgres-password",
    ownerUsername: `${databaseName}_owner`,
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
  });
  addBackupPolicy(state, "postgres", id);
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
  const resource: ObjectStore = {
    accessKey: "MOCK_ONLY_ACCESS_KEY",
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
    secret: "mock-only-object-secret",
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
  });
  addBackupPolicy(state, "object_store", id);
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
