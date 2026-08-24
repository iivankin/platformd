import type {
  ManagedPostgres,
  ManagedRedis,
  ObjectMetadata,
  ObjectStore,
} from "../web/api";
import { json, mockError, noContent } from "./http";
import {
  mockManagedStatsHistory,
  mockPostgresStats,
  mockRedisStats,
} from "./managed-stats";
import { handlePostgresQuery } from "./postgres-query";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

type ManagedCollection = "object-stores" | "postgres" | "redis";

const isManagedCollection = (value: string): value is ManagedCollection =>
  value === "object-stores" || value === "postgres" || value === "redis";

const portForwardChangedCode = (collection: ManagedCollection) => {
  if (collection === "postgres") {
    return "postgres_changed";
  }
  if (collection === "redis") {
    return "redis_changed";
  }
  return "object_store_changed";
};

const projectCountField = (collection: ManagedCollection) => {
  if (collection === "postgres") {
    return "postgresCount" as const;
  }
  if (collection === "redis") {
    return "redisCount" as const;
  }
  return "objectStoreCount" as const;
};

const managedResource = (
  state: MockState,
  collection: ManagedCollection,
  resourceID: string
) => {
  if (collection === "redis") {
    return state.redis[resourceID];
  }
  if (collection === "postgres") {
    return state.postgres[resourceID];
  }
  return state.objectStores[resourceID];
};

const handleManagedResource = async (
  request: Request,
  state: MockState,
  collection: string,
  resourceID: string,
  rest: string[]
): Promise<Response | undefined> => {
  if (rest.length > 0 || !isManagedCollection(collection)) {
    return undefined;
  }
  const resource = managedResource(state, collection, resourceID);
  if (!resource) {
    return mockError("not_found", "Managed resource not found", 404);
  }
  if (request.method === "GET") {
    return json(resource);
  }
  if (request.method !== "DELETE") {
    return undefined;
  }
  const body = (await request.json()) as { expectedUpdatedAt?: unknown };
  if (body.expectedUpdatedAt !== resource.updatedAt) {
    return mockError(
      portForwardChangedCode(collection),
      "Managed resource changed",
      409
    );
  }
  if (collection === "postgres") {
    Reflect.deleteProperty(state.postgres, resourceID);
    Reflect.deleteProperty(state.postgresExtensions, resourceID);
  } else if (collection === "redis") {
    Reflect.deleteProperty(state.redis, resourceID);
  } else {
    Reflect.deleteProperty(state.objectStores, resourceID);
    Reflect.deleteProperty(state.objectMetadata, resourceID);
  }
  Reflect.deleteProperty(state.runtimeDeployments, resourceID);
  Reflect.deleteProperty(state.logs, resourceID);
  Reflect.deleteProperty(state.containerFiles, `${collection}:${resourceID}`);
  Reflect.deleteProperty(state.containerPorts, `${collection}:${resourceID}`);
  state.operations = Object.fromEntries(
    Object.entries(state.operations).filter(
      ([, operation]) => operation.targetId !== resourceID
    )
  );
  state.backupPolicies = state.backupPolicies.filter(
    (policy) => policy.resourceId !== resourceID
  );
  const canvas = state.canvases[resource.projectId];
  if (canvas) {
    canvas.resources = canvas.resources.filter(
      (candidate) => candidate.id !== resourceID
    );
    canvas.connections = canvas.connections.filter(
      (connection) =>
        connection.sourceId !== resourceID && connection.targetId !== resourceID
    );
    const countField = projectCountField(collection);
    canvas.project[countField] = Math.max(0, canvas.project[countField] - 1);
    canvas.project.updatedAt = mockNow();
  }
  return noContent();
};

const supportsPortForward = (
  collection: string
): collection is ManagedCollection =>
  collection === "postgres" ||
  collection === "object-stores" ||
  collection === "redis";

const parsedPortForward = (value: unknown) => {
  if (typeof value !== "object" || value === null) {
    return;
  }
  const body = value as { repository?: string; workflows?: string[] };
  if (typeof body.repository !== "string") {
    return;
  }
  return {
    repository: body.repository.trim().toLowerCase(),
    workflows: Array.isArray(body.workflows)
      ? body.workflows.filter(
          (entry): entry is string => typeof entry === "string"
        )
      : [],
  };
};

const handleManagedPortForward = async (
  request: Request,
  state: MockState,
  collection: string,
  resourceID: string,
  rest: string[]
): Promise<Response | undefined> => {
  if (
    request.method !== "PUT" ||
    rest.length !== 1 ||
    rest[0] !== "port-forward" ||
    !supportsPortForward(collection)
  ) {
    return undefined;
  }
  const resource = managedResource(state, collection, resourceID);
  if (!resource) {
    return mockError("not_found", "Managed resource not found", 404);
  }
  const body = (await request.json()) as {
    expectedUpdatedAt?: unknown;
    portForward?: unknown;
  };
  if (
    typeof body.expectedUpdatedAt !== "number" ||
    body.expectedUpdatedAt !== resource.updatedAt
  ) {
    return mockError(
      portForwardChangedCode(collection),
      "Managed resource changed",
      409
    );
  }
  const next = parsedPortForward(body.portForward);
  const portForward = next?.repository ? next : undefined;
  const updatedAt = mockNow();
  if (collection === "postgres") {
    const updated = {
      ...(resource as ManagedPostgres),
      portForward,
      updatedAt,
    };
    state.postgres[resourceID] = updated;
    return json(updated);
  }
  if (collection === "redis") {
    const updated = { ...(resource as ManagedRedis), portForward, updatedAt };
    state.redis[resourceID] = updated;
    return json(updated);
  }
  const updated = { ...(resource as ObjectStore), portForward, updatedAt };
  state.objectStores[resourceID] = updated;
  return json(updated);
};

const handleObjectStorePublicAccess = async (
  request: Request,
  state: MockState,
  collection: string,
  resourceID: string,
  rest: string[]
): Promise<Response | undefined> => {
  if (
    request.method !== "PUT" ||
    collection !== "object-stores" ||
    rest.length !== 1 ||
    rest[0] !== "public-access"
  ) {
    return undefined;
  }
  const resource = managedResource(state, collection, resourceID) as
    | ObjectStore
    | undefined;
  if (!resource) {
    return mockError("not_found", "Managed resource not found", 404);
  }
  const body = (await request.json()) as {
    corsOrigins?: unknown;
    expectedUpdatedAt?: unknown;
    publicHostname?: unknown;
  };
  if (
    typeof body.expectedUpdatedAt !== "number" ||
    body.expectedUpdatedAt !== resource.updatedAt
  ) {
    return mockError("object_store_changed", "Object store changed", 409);
  }
  const corsOrigins = Array.isArray(body.corsOrigins)
    ? body.corsOrigins.filter(
        (entry): entry is string => typeof entry === "string"
      )
    : [];
  const publicHostname =
    typeof body.publicHostname === "string" ? body.publicHostname.trim() : "";
  const updated: ObjectStore = {
    ...resource,
    corsOrigins,
    publicHostname: publicHostname || undefined,
    updatedAt: mockNow(),
  };
  state.objectStores[resourceID] = updated;
  return json(updated);
};

const handleManagedLogs = (
  request: Request,
  state: MockState,
  collection: string,
  resourceID: string,
  rest: string[]
): Response | undefined => {
  const [resource, ...tail] = rest;
  if (
    request.method !== "GET" ||
    !isManagedCollection(collection) ||
    collection === "object-stores" ||
    resource !== "logs" ||
    tail.length > 0
  ) {
    return undefined;
  }
  const window = state.logs[resourceID] ?? { records: [], truncated: false };
  const url = new URL(request.url);
  const deploymentID = url.searchParams.get("deploymentId");
  const contains = url.searchParams.get("contains");
  return json({
    records: window.records
      .filter((record) => !deploymentID || record.deploymentId === deploymentID)
      .filter((record) => !contains || record.text.includes(contains)),
    truncated: window.truncated,
  });
};

const handleManagedDeployments = (
  request: Request,
  state: MockState,
  collection: string,
  resourceID: string,
  rest: string[]
): Response | undefined => {
  if (collection !== "postgres" && collection !== "redis") {
    return undefined;
  }
  const [resource, deploymentID, action, ...tail] = rest;
  if (resource !== "deployments" || tail.length > 0) {
    return undefined;
  }
  const deployments = state.runtimeDeployments[resourceID] ?? [];
  if (request.method === "GET" && !deploymentID) {
    return json({ deployments });
  }
  const deployment = deployments.find(
    (candidate) => candidate.id === deploymentID
  );
  if (!deployment) {
    return mockError("deployment_not_found", "Deployment not found", 404);
  }
  if (request.method === "GET" && !action) {
    return json(deployment);
  }
  if (
    request.method !== "POST" ||
    (action !== "remove" && action !== "restart")
  ) {
    return undefined;
  }
  if (action === "restart") {
    deployment.status = "succeeded";
    deployment.finishedAt = mockNow();
    return noContent();
  }
  if (deployment.active) {
    deployment.status = "removed";
    deployment.finishedAt = mockNow();
  } else {
    state.runtimeDeployments[resourceID] = deployments.filter(
      (candidate) => candidate.id !== deployment.id
    );
    const window = state.logs[resourceID];
    if (window) {
      window.records = window.records.filter(
        (record) => record.deploymentId !== deployment.id
      );
    }
  }
  return noContent();
};

const handleRedisStats = (
  request: Request,
  collection: string,
  rest: string[],
  url: URL
): Response | undefined => {
  if (collection !== "redis" || request.method !== "GET") {
    return undefined;
  }
  const [resource, detail, ...tail] = rest;
  if (resource !== "stats" || tail.length > 0) {
    return undefined;
  }
  if (detail === "history") {
    return mockManagedStatsHistory("redis", url.searchParams.get("range"));
  }
  if (detail) {
    return undefined;
  }
  return mockRedisStats();
};

const handleRedisData = (
  request: Request,
  collection: string,
  rest: string[]
): Response | undefined => {
  if (collection !== "redis") {
    return undefined;
  }
  const [resource, detail, ...tail] = rest;
  if (request.method === "GET" && resource === "persistence" && !detail) {
    return json({
      actualRpoMillis: 15_000,
      backgroundSaveInProgress: false,
      lastBackgroundSaveSuccessful: true,
      lastSuccessfulSaveAt: mockNow() - 15_000,
      needsAttention: false,
      observedAt: mockNow(),
      targetRpoMillis: 60_000,
    });
  }
  if (request.method === "GET" && resource === "keys" && !detail) {
    return json({
      keys: [
        {
          keyBase64: btoa("session:demo"),
          keyText: "session:demo",
          sizeBytes: 128,
          type: "string",
        },
      ],
      nextCursor: "0",
    });
  }
  if (request.method === "GET" && resource === "preview" && !detail) {
    return json({
      items: [{ values: [{ base64: btoa("mock-value"), text: "mock-value" }] }],
      length: 1,
      nextCursor: "0",
      truncated: false,
      type: "string",
    });
  }
  if (
    request.method === "POST" &&
    resource === "data" &&
    detail === "mutations" &&
    tail.length === 0
  ) {
    return json({ affected: 1, auditRecorded: true, streamId: "" });
  }
  return undefined;
};

const handlePostgresStats = (
  request: Request,
  collection: string,
  rest: string[],
  url: URL
): Response | undefined => {
  if (collection !== "postgres" || request.method !== "GET") {
    return undefined;
  }
  const [resource, detail, ...tail] = rest;
  if (resource !== "stats" || tail.length > 0) {
    return undefined;
  }
  if (detail === "history") {
    return mockManagedStatsHistory("postgres", url.searchParams.get("range"));
  }
  if (detail) {
    return undefined;
  }
  return mockPostgresStats();
};

const handlePostgresExtensions = (
  request: Request,
  state: MockState,
  collection: string,
  resourceID: string,
  rest: string[]
): Response | undefined => {
  if (collection !== "postgres") {
    return undefined;
  }
  const [resource, extensionName, ...tail] = rest;
  if (resource !== "extensions" || tail.length > 0) {
    return undefined;
  }
  const extensions = state.postgresExtensions[resourceID] ?? [];
  if (request.method === "GET" && !extensionName) {
    return json({ extensions });
  }
  if (
    !extensionName ||
    (request.method !== "PUT" && request.method !== "DELETE")
  ) {
    return undefined;
  }
  const extension = extensions.find(
    (candidate) => candidate.name === extensionName
  );
  if (!extension) {
    return mockError(
      "invalid_managed_postgres",
      "Extension is not available in this PostgreSQL image",
      400
    );
  }
  if (request.method === "PUT") {
    extension.installedVersion = extension.defaultVersion;
  } else {
    delete extension.installedVersion;
  }
  state.sequence += 1;
  const timestamp = Date.now();
  const operation = {
    finishedAt: timestamp,
    id: `operation-postgres-extension-${state.sequence}`,
    kind:
      request.method === "PUT"
        ? "postgres_extension_install"
        : "postgres_extension_uninstall",
    progress: "complete",
    startedAt: timestamp,
    status: "succeeded" as const,
    targetId: resourceID,
  };
  state.operations[operation.id] = operation;
  return json(operation, 202);
};

const browseObjectPage = (
  available: ObjectMetadata[],
  prefix: string,
  delimiter: string
) => {
  const objects: ObjectMetadata[] = [];
  const prefixes = new Set<string>();
  for (const object of available) {
    if (!object.objectKey.startsWith(prefix)) {
      continue;
    }
    const remainder = object.objectKey.slice(prefix.length);
    const delimiterIndex = delimiter ? remainder.indexOf(delimiter) : -1;
    if (delimiterIndex < 0) {
      objects.push(object);
      continue;
    }
    prefixes.add(
      `${prefix}${remainder.slice(0, delimiterIndex + delimiter.length)}`
    );
  }
  return { objects, prefixes: [...prefixes].toSorted() };
};

const handleObjects = (
  request: Request,
  state: MockState,
  storeID: string,
  rest: string[],
  url: URL
): Response | undefined => {
  const [resource, detail, ...tail] = rest;
  if (resource !== "objects" || tail.length > 0) {
    return undefined;
  }
  const key = url.searchParams.get("key") ?? "mock-object";
  if (request.method === "GET" && detail === "preview") {
    const metadata = (state.objectMetadata[storeID] ?? []).find(
      (object) => object.objectKey === key
    );
    return metadata
      ? json({
          allowed: metadata.contentType === "application/json",
          metadata,
          ...(metadata.contentType === "application/json"
            ? { text: '{"mock":true}' }
            : {}),
        })
      : mockError("not_found", "Object not found", 404);
  }
  if (detail) {
    return undefined;
  }
  if (request.method === "GET") {
    const prefix = url.searchParams.get("prefix") ?? "";
    const delimiter = url.searchParams.get("delimiter") ?? "";
    const page = browseObjectPage(
      state.objectMetadata[storeID] ?? [],
      prefix,
      delimiter
    );
    return json({
      nextContinuationToken: "",
      ...page,
    });
  }
  if (request.method === "DELETE") {
    state.objectMetadata[storeID] = (
      state.objectMetadata[storeID] ?? []
    ).filter((object) => object.objectKey !== key);
    return noContent();
  }
  if (request.method !== "PUT") {
    return undefined;
  }
  const metadata = {
    contentType: request.headers.get("Content-Type") ?? undefined,
    createdAt: mockNow(),
    etag: nextMockID(state, "etag"),
    objectKey: key,
    size: Number(request.headers.get("Content-Length") ?? 0),
    updatedAt: mockNow(),
  };
  state.objectMetadata[storeID] = [
    metadata,
    ...(state.objectMetadata[storeID] ?? []).filter(
      (object) => object.objectKey !== key
    ),
  ];
  return json(metadata);
};

const objectSizeHistogram = (sizes: number[]) => {
  const upperBounds = [
    1024,
    1024 ** 2,
    10 * 1024 ** 2,
    64 * 1024 ** 2,
    128 * 1024 ** 2,
    512 * 1024 ** 2,
    Number.POSITIVE_INFINITY,
  ];
  const buckets = [
    { count: 0, label: "0–1 KiB" },
    { count: 0, label: "1 KiB–1 MiB" },
    { count: 0, label: "1–10 MiB" },
    { count: 0, label: "10–64 MiB" },
    { count: 0, label: "64–128 MiB" },
    { count: 0, label: "128–512 MiB" },
    { count: 0, label: "512 MiB+" },
  ];
  for (const size of sizes) {
    const index = upperBounds.findIndex((upperBound) => size < upperBound);
    const bucket = buckets[index];
    if (bucket) {
      bucket.count += 1;
    }
  }
  return buckets;
};

const handleObjectStoreStatistics = (
  request: Request,
  state: MockState,
  storeID: string,
  rest: string[],
  url: URL
): Response | undefined => {
  const [resource, detail, ...tail] = rest;
  if (tail.length > 0) {
    return undefined;
  }
  const objects = state.objectMetadata[storeID] ?? [];
  if (
    request.method === "GET" &&
    resource === "stats" &&
    detail === "history"
  ) {
    return mockManagedStatsHistory(
      "object_store",
      url.searchParams.get("range")
    );
  }
  if (request.method === "GET" && resource === "stats" && !detail) {
    const sizes = objects.map((object) => object.size);
    return json({
      objectCount: objects.length,
      objectSizeHistogram: objectSizeHistogram(sizes),
      observedAt: mockNow(),
      ready: true,
      totalBytes: sizes.reduce((total, size) => total + size, 0),
    });
  }
  if (resource !== "largest-objects" || detail) {
    return undefined;
  }
  if (request.method === "GET") {
    return json({ objects: [], scannedObjects: 0, status: "idle" });
  }
  if (request.method === "DELETE") {
    return json({ objects: [], scannedObjects: 0, status: "cancelled" });
  }
  if (request.method === "POST") {
    return json(
      {
        objects: objects
          .map((object) => ({ key: object.objectKey, size: object.size }))
          .toSorted(
            (left, right) =>
              right.size - left.size || left.key.localeCompare(right.key)
          )
          .slice(0, 10),
        scannedObjects: objects.length,
        status: "complete",
      },
      202
    );
  }
  return undefined;
};

const handleManagedImageTags = (
  request: Request,
  segments: string[]
): Response | undefined => {
  const [root, engine, resource, ...tail] = segments;
  if (
    request.method !== "GET" ||
    root !== "managed-images" ||
    (engine !== "postgres" && engine !== "redis") ||
    resource !== "tags" ||
    tail.length > 0
  ) {
    return undefined;
  }
  const tag = engine === "postgres" ? "17.5" : "8.2";
  return json({
    page: 1,
    pageSize: 50,
    tags: [
      {
        lastUpdated: new Date(mockNow()).toISOString(),
        name: tag,
        platforms: [
          {
            architecture: "amd64",
            digest: `sha256:${engine}-mock`,
            os: "linux",
            sizeBytes: 125_829_120,
          },
        ],
      },
    ],
    total: 1,
  });
};

export const handleManagedResourcesAPI = async (
  request: Request,
  state: MockState,
  segments: string[],
  url: URL
): Promise<Response | undefined> => {
  const imageTagsResponse = handleManagedImageTags(request, segments);
  if (imageTagsResponse) {
    return imageTagsResponse;
  }
  const [root, projectID, collection, resourceID, ...rest] = segments;
  if (root !== "projects" || !projectID || !collection || !resourceID) {
    return undefined;
  }
  return (
    (await handleManagedResource(
      request,
      state,
      collection,
      resourceID,
      rest
    )) ??
    (await handleManagedPortForward(
      request,
      state,
      collection,
      resourceID,
      rest
    )) ??
    (await handleObjectStorePublicAccess(
      request,
      state,
      collection,
      resourceID,
      rest
    )) ??
    handleManagedDeployments(request, state, collection, resourceID, rest) ??
    handleManagedLogs(request, state, collection, resourceID, rest) ??
    handleRedisStats(request, collection, rest, url) ??
    handleRedisData(request, collection, rest) ??
    handlePostgresStats(request, collection, rest, url) ??
    handlePostgresExtensions(request, state, collection, resourceID, rest) ??
    (await handlePostgresQuery(request, collection, rest)) ??
    (collection === "object-stores"
      ? (handleObjectStoreStatistics(request, state, resourceID, rest, url) ??
        handleObjects(request, state, resourceID, rest, url))
      : undefined)
  );
};
