import type {
  Project,
  ProjectWebhook,
  ProjectWebhookEventType,
} from "../web/api";
import {
  apiSegments,
  json,
  mockError,
  noContent,
  readObject,
  stringField,
} from "./http";
import { handleManagedResourcesAPI } from "./managed-resources";
import { handleResourceCreation } from "./project-resources";
import { handleServicesAPI } from "./services";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

const handleProjectCollection = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, ...rest] = segments;
  if (root !== "projects" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET") {
    return json(state.projects);
  }
  if (request.method !== "POST") {
    return undefined;
  }
  const input = await readObject(request);
  const project: Project = {
    createdAt: mockNow(),
    hasIcon: false,
    id: nextMockID(state, "project"),
    name: stringField(input, "name", "mock-project"),
    networkGatewayCount: 0,
    objectStoreCount: 0,
    postgresCount: 0,
    redisCount: 0,
    serviceCount: 0,
    updatedAt: mockNow(),
  };
  state.projects = [...state.projects, project];
  state.canvases[project.id] = {
    connections: [],
    project,
    resources: [],
  };
  return json(project, 201);
};

const handleCanvas = (
  request: Request,
  state: MockState,
  segments: string[]
): Response | undefined => {
  const [root, projectID, resource, ...rest] = segments;
  if (
    request.method !== "GET" ||
    root !== "projects" ||
    !projectID ||
    resource !== "canvas" ||
    rest.length > 0
  ) {
    return undefined;
  }
  return state.canvases[projectID]
    ? json(state.canvases[projectID])
    : mockError("not_found", "Project not found", 404);
};

// 1x1 PNG
const mockProjectIconBytes = Uint8Array.from(
  atob(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
  ),
  (char) => char.codePointAt(0) ?? 0
);

const handleProjectIcon = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, projectID, resource, ...rest] = segments;
  if (
    root !== "projects" ||
    !projectID ||
    resource !== "icon" ||
    rest.length > 0
  ) {
    return undefined;
  }
  const index = state.projects.findIndex((entry) => entry.id === projectID);
  if (index === -1) {
    return mockError("project_not_found", "Project not found", 404);
  }
  const current = state.projects[index];
  if (!current) {
    return mockError("project_not_found", "Project not found", 404);
  }
  if (request.method === "GET") {
    if (!current.hasIcon) {
      return mockError("project_icon_not_found", "Project icon not found", 404);
    }
    return new Response(mockProjectIconBytes, {
      headers: {
        "Cache-Control": "private, max-age=60",
        "Content-Type": "image/png",
        "X-Content-Type-Options": "nosniff",
      },
    });
  }
  if (request.method === "PUT") {
    const body = new Uint8Array(await request.arrayBuffer());
    if (body.byteLength === 0 || body.byteLength > 131_072) {
      return mockError(
        "invalid_project_icon",
        "Project icon must be a PNG, JPEG, or WebP image up to 128 KiB"
      );
    }
    const updated: Project = {
      ...current,
      hasIcon: true,
      updatedAt: mockNow(),
    };
    state.projects = state.projects.with(index, updated);
    const canvas = state.canvases[projectID];
    if (canvas) {
      state.canvases[projectID] = { ...canvas, project: updated };
    }
    return json(updated);
  }
  if (request.method === "DELETE") {
    if (!current.hasIcon) {
      return mockError("project_icon_not_found", "Project icon not found", 404);
    }
    const updated: Project = {
      ...current,
      hasIcon: false,
      updatedAt: mockNow(),
    };
    state.projects = state.projects.with(index, updated);
    const canvas = state.canvases[projectID];
    if (canvas) {
      state.canvases[projectID] = { ...canvas, project: updated };
    }
    return json(updated);
  }
  return undefined;
};

const projectWebhookEventTypes: ProjectWebhookEventType[] = [
  "deployment.started",
  "deployment.succeeded",
  "deployment.failed",
  "deployment.interrupted",
  "deployment.skipped",
];

const webhookInput = async (request: Request) => {
  const input = await readObject(request);
  const eventTypes = Array.isArray(input.eventTypes)
    ? input.eventTypes.filter(
        (value): value is ProjectWebhookEventType =>
          typeof value === "string" &&
          projectWebhookEventTypes.includes(value as ProjectWebhookEventType)
      )
    : [];
  return { eventTypes, url: stringField(input, "url") };
};

const isProjectWebhooksPath = (segments: string[]) => {
  const [root, projectID, collection] = segments;
  return (
    root === "projects" &&
    Boolean(projectID) &&
    collection === "webhooks" &&
    segments.length <= 4
  );
};

const handleProjectWebhooks = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  if (!isProjectWebhooksPath(segments)) {
    return undefined;
  }
  const [, projectID = "", , webhookID] = segments;
  if (!state.projects.some((project) => project.id === projectID)) {
    return mockError("project_not_found", "Project not found", 404);
  }
  if (request.method === "GET" && !webhookID) {
    return json({
      eventTypes: projectWebhookEventTypes,
      webhooks: state.projectWebhooks[projectID] ?? [],
    });
  }
  if (request.method === "POST" && webhookID === "test") {
    const input = await webhookInput(request);
    return input.url
      ? noContent()
      : mockError("invalid_webhook_url", "Webhook URL is required");
  }
  if (request.method === "POST" && !webhookID) {
    const input = await webhookInput(request);
    if (!input.url || input.eventTypes.length === 0) {
      return mockError("invalid_webhook", "URL and event types are required");
    }
    const webhook: ProjectWebhook = {
      createdAt: mockNow(),
      eventTypes: input.eventTypes,
      id: nextMockID(state, "webhook"),
      projectId: projectID,
      updatedAt: mockNow(),
      url: input.url,
    };
    state.projectWebhooks[projectID] = [
      ...(state.projectWebhooks[projectID] ?? []),
      webhook,
    ];
    return json(webhook, 201);
  }
  const current = state.projectWebhooks[projectID] ?? [];
  const existing = current.find((webhook) => webhook.id === webhookID);
  if (!existing) {
    return mockError(
      "project_webhook_not_found",
      "Project webhook not found",
      404
    );
  }
  if (request.method === "PUT") {
    const input = await webhookInput(request);
    const updated: ProjectWebhook = {
      ...existing,
      eventTypes: input.eventTypes,
      updatedAt: mockNow(),
      url: input.url,
    };
    state.projectWebhooks[projectID] = current.map((webhook) =>
      webhook.id === webhookID ? updated : webhook
    );
    return json(updated);
  }
  if (request.method === "DELETE") {
    state.projectWebhooks[projectID] = current.filter(
      (webhook) => webhook.id !== webhookID
    );
    return noContent();
  }
  return undefined;
};

const withoutKeys = <Value>(
  values: Record<string, Value>,
  keys: ReadonlySet<string>
): Record<string, Value> =>
  Object.fromEntries(Object.entries(values).filter(([key]) => !keys.has(key)));

const withoutResourceBackups = <Value>(
  values: Record<string, Value>,
  resourceIDs: ReadonlySet<string>
): Record<string, Value> =>
  Object.fromEntries(
    Object.entries(values).filter(([key]) =>
      [...resourceIDs].every((resourceID) => !key.includes(`:${resourceID}@`))
    )
  );

const removeProjectResources = (state: MockState, projectID: string): void => {
  const serviceIDs = new Set(
    Object.values(state.services)
      .filter((service) => service.projectId === projectID)
      .map((service) => service.id)
  );
  const resourceIDs = new Set([
    ...serviceIDs,
    ...Object.values(state.postgres)
      .filter((resource) => resource.projectId === projectID)
      .map((resource) => resource.id),
    ...Object.values(state.redis)
      .filter((resource) => resource.projectId === projectID)
      .map((resource) => resource.id),
    ...Object.values(state.objectStores)
      .filter((resource) => resource.projectId === projectID)
      .map((resource) => resource.id),
    ...Object.values(state.networkGateways)
      .filter((resource) => resource.projectId === projectID)
      .map((resource) => resource.id),
    ...[...serviceIDs].flatMap((serviceID) =>
      (state.volumes[serviceID] ?? []).map((volume) => volume.id)
    ),
  ]);

  state.services = withoutKeys(state.services, resourceIDs);
  state.postgres = withoutKeys(state.postgres, resourceIDs);
  state.redis = withoutKeys(state.redis, resourceIDs);
  state.objectStores = withoutKeys(state.objectStores, resourceIDs);
  state.networkGateways = withoutKeys(state.networkGateways, resourceIDs);
  state.objectMetadata = withoutKeys(state.objectMetadata, resourceIDs);
  state.postgresExtensions = withoutKeys(state.postgresExtensions, resourceIDs);
  state.runtimeDeployments = withoutKeys(state.runtimeDeployments, resourceIDs);
  state.logs = withoutKeys(state.logs, resourceIDs);
  state.deployments = withoutKeys(state.deployments, serviceIDs);
  state.previews = withoutKeys(state.previews, serviceIDs);
  state.domains = withoutKeys(state.domains, serviceIDs);
  state.listeners = withoutKeys(state.listeners, serviceIDs);
  state.volumes = withoutKeys(state.volumes, serviceIDs);
  state.containerFiles = withoutKeys(state.containerFiles, serviceIDs);
  state.containerPorts = withoutKeys(state.containerPorts, serviceIDs);
  state.operations = Object.fromEntries(
    Object.entries(state.operations).filter(
      ([, operation]) => !resourceIDs.has(operation.targetId)
    )
  );
  state.backupGenerations = withoutResourceBackups(
    state.backupGenerations,
    resourceIDs
  );
  state.backupHistory = withoutResourceBackups(
    state.backupHistory,
    resourceIDs
  );
  state.backupPolicies = state.backupPolicies.filter(
    (policy) => !resourceIDs.has(policy.resourceId)
  );
};

const handleProjectDelete = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, projectID, ...rest] = segments;
  if (
    request.method !== "DELETE" ||
    root !== "projects" ||
    !projectID ||
    rest.length > 0
  ) {
    return undefined;
  }
  const project = state.projects.find((entry) => entry.id === projectID);
  if (!project) {
    return mockError("project_not_found", "Project not found", 404);
  }
  const input = await readObject(request);
  if (stringField(input, "expectedName", "") !== project.name) {
    return mockError("project_changed", "Project name changed", 409);
  }
  removeProjectResources(state, projectID);
  state.projects = state.projects.filter((entry) => entry.id !== projectID);
  state.projectWebhooks = withoutKeys(
    state.projectWebhooks,
    new Set([projectID])
  );
  state.tokens = state.tokens.filter((token) => token.projectId !== projectID);
  state.canvases = withoutKeys(state.canvases, new Set([projectID]));
  return new Response(null, { status: 204 });
};

export const handleProjectsAPI = async (
  request: Request,
  state: MockState,
  pathname: string,
  url: URL
): Promise<Response | undefined> => {
  const segments = apiSegments(pathname);
  return (
    (await handleProjectCollection(request, state, segments)) ??
    (await handleProjectIcon(request, state, segments)) ??
    (await handleProjectDelete(request, state, segments)) ??
    (await handleProjectWebhooks(request, state, segments)) ??
    handleCanvas(request, state, segments) ??
    (await handleResourceCreation(request, state, segments)) ??
    (await handleServicesAPI(request, state, segments, url)) ??
    handleManagedResourcesAPI(request, state, segments, url)
  );
};
