import { json, mockError, noContent } from "./http";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

type ManagedCollection = "object-stores" | "postgres" | "redis";

const isManagedCollection = (value: string): value is ManagedCollection =>
  value === "object-stores" || value === "postgres" || value === "redis";

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

const handleManagedResource = (
  request: Request,
  state: MockState,
  collection: string,
  resourceID: string,
  rest: string[]
): Response | undefined => {
  if (
    request.method !== "GET" ||
    rest.length > 0 ||
    !isManagedCollection(collection)
  ) {
    return undefined;
  }
  const resource = managedResource(state, collection, resourceID);
  return resource
    ? json(resource)
    : mockError("not_found", "Managed resource not found", 404);
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
  if (request.method === "GET" && resource === "stats" && !detail) {
    return json({
      aofEnabled: true,
      blockedClients: 0,
      commands: [
        {
          calls: 18_700,
          microsPerCall: 8.4,
          name: "get",
          totalMicros: 157_080,
        },
        { calls: 9500, microsPerCall: 11, name: "set", totalMicros: 104_500 },
      ],
      connectedClients: 12,
      evictedKeys: 0,
      evictionPolicy: "noeviction",
      expiredKeys: 1300,
      fragmentationRatio: 1.08,
      keyspaceHits: 19_700_000,
      keyspaceMisses: 810_000,
      keyspaces: [
        {
          averageTtlMillis: 2_160_000,
          database: "db0",
          expires: 1300,
          keys: 25_300,
        },
      ],
      maxMemoryBytes: 0,
      operationsPerSecond: 539,
      peakMemoryBytes: 83_330_000,
      rejectedConnections: 0,
      rssMemoryBytes: 48_600_000,
      totalCommands: 141_200_000,
      totalConnections: 9500,
      uptimeSeconds: 8_733_600,
      usedMemoryBytes: 46_200_000,
      version: "8.2.1",
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

const handlePostgresQuery = (
  request: Request,
  collection: string,
  rest: string[]
): Response | undefined => {
  const [resource, ...tail] = rest;
  if (
    request.method !== "POST" ||
    collection !== "postgres" ||
    resource !== "query" ||
    tail.length > 0
  ) {
    return undefined;
  }
  return json({
    auditRecorded: true,
    statements: [
      {
        columns: [{ name: "status", typeOid: 25 }],
        commandTag: "SELECT 1",
        rows: [[{ text: "mock backend ready" }]],
        truncated: false,
      },
    ],
    truncated: false,
  });
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
    return json({
      nextContinuationToken: "",
      objects: (state.objectMetadata[storeID] ?? []).filter((object) =>
        object.objectKey.startsWith(prefix)
      ),
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

export const handleManagedResourcesAPI = (
  request: Request,
  state: MockState,
  segments: string[],
  url: URL
): Response | undefined => {
  const imageTagsResponse = handleManagedImageTags(request, segments);
  if (imageTagsResponse) {
    return imageTagsResponse;
  }
  const [root, projectID, collection, resourceID, ...rest] = segments;
  if (root !== "projects" || !projectID || !collection || !resourceID) {
    return undefined;
  }
  return (
    handleManagedResource(request, state, collection, resourceID, rest) ??
    handleManagedDeployments(request, state, collection, resourceID, rest) ??
    handleManagedLogs(request, state, collection, resourceID, rest) ??
    handleRedisData(request, collection, rest) ??
    handlePostgresExtensions(request, state, collection, resourceID, rest) ??
    handlePostgresQuery(request, collection, rest) ??
    (collection === "object-stores"
      ? handleObjects(request, state, resourceID, rest, url)
      : undefined)
  );
};
