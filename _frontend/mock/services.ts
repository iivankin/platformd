import type { Service } from "../web/api";
import { logFieldFiltersSchema } from "../web/log-field-filter";
import type { LogFieldFilter } from "../web/log-field-filter";
import {
  booleanField,
  json,
  mockError,
  noContent,
  numberField,
  readObject,
  stringField,
} from "./http";
import { stringRecord } from "./project-helpers";
import { handleServiceTelemetry } from "./service-telemetry";
import {
  mockDomainOutputs,
  referencedResourceNames,
  resolveMockEnvironment,
} from "./service-variables";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

const withoutRecordKey = <Value>(
  record: Record<string, Value>,
  key: string
): Record<string, Value> =>
  Object.fromEntries(
    Object.entries(record).filter(([candidate]) => candidate !== key)
  );

const updateServiceRegistryCredential = (
  service: Service,
  input: Record<string, unknown>
) => {
  const credential = input.registryCredential;
  if (
    service.source.type === "private_image" &&
    typeof credential === "object" &&
    credential !== null
  ) {
    service.registryCredential = {
      ...(credential as { password: string; username: string }),
      registryHost:
        service.source.image.reference.split("/", 1)[0] ?? "docker.io",
    };
    return;
  }
  if (service.source.type !== "private_image") {
    service.registryCredential = undefined;
  }
};

const updateServiceBeforeDeploy = (
  service: Service,
  input: Record<string, unknown>
) => {
  service.beforeDeploy =
    typeof input.beforeDeploy === "object" && input.beforeDeploy !== null
      ? (input.beforeDeploy as Service["beforeDeploy"])
      : undefined;
};

const updateServicePortForward = (
  service: Service,
  input: Record<string, unknown>
) => {
  service.portForward =
    typeof input.portForward === "object" && input.portForward !== null
      ? (input.portForward as Service["portForward"])
      : undefined;
};

const updateServiceHostId = (
  service: Service,
  input: Record<string, unknown>
) => {
  if (typeof input.hostId === "string") {
    service.hostId = input.hostId || undefined;
  }
};

const handleService = async (
  request: Request,
  state: MockState,
  serviceID: string,
  rest: string[]
): Promise<Response | undefined> => {
  if (rest.length > 0) {
    return undefined;
  }
  const service = state.services[serviceID];
  if (!service) {
    return mockError("not_found", "Service not found", 404);
  }
  if (request.method === "GET") {
    return json(service);
  }
  if (request.method === "DELETE") {
    state.services = withoutRecordKey(state.services, serviceID);
    state.deployments = withoutRecordKey(state.deployments, serviceID);
    state.domains = withoutRecordKey(state.domains, serviceID);
    state.listeners = withoutRecordKey(state.listeners, serviceID);
    state.logs = withoutRecordKey(state.logs, serviceID);
    state.volumes = withoutRecordKey(state.volumes, serviceID);
    state.serviceErrors = withoutRecordKey(state.serviceErrors, serviceID);
    state.metricCharts = withoutRecordKey(state.metricCharts, serviceID);
    state.serviceTelemetry = withoutRecordKey(
      state.serviceTelemetry,
      serviceID
    );
    const canvas = state.canvases[service.projectId];
    if (canvas) {
      canvas.resources = canvas.resources.filter(
        (resource) => resource.id !== serviceID
      );
      canvas.connections = canvas.connections.filter(
        (connection) =>
          connection.sourceId !== serviceID && connection.targetId !== serviceID
      );
      canvas.project.serviceCount = Math.max(
        0,
        canvas.project.serviceCount - 1
      );
    }
    return noContent();
  }
  if (request.method !== "PUT") {
    return undefined;
  }
  const input = await readObject(request);
  service.enabled = booleanField(input, "enabled", service.enabled);
  updateServiceBeforeDeploy(service, input);
  updateServicePortForward(service, input);
  if (typeof input.source === "object" && input.source !== null) {
    service.source = input.source as Service["source"];
  }
  updateServiceRegistryCredential(service, input);
  service.environment = stringRecord(input.environment);
  service.healthCheck =
    typeof input.healthCheck === "object" && input.healthCheck !== null
      ? (input.healthCheck as Service["healthCheck"])
      : undefined;
  service.volumeMounts = Array.isArray(input.volumeMounts)
    ? (input.volumeMounts as Service["volumeMounts"])
    : service.volumeMounts;
  updateServiceHostId(service, input);
  service.updatedAt = mockNow();
  const canvas = state.canvases[service.projectId];
  if (canvas) {
    const canvasService = canvas.resources.find(
      (resource) => resource.id === serviceID
    );
    if (canvasService) {
      canvasService.hostId = service.hostId;
      canvasService.hostName = state.hosts.find(
        (host) => host.id === service.hostId
      )?.name;
      const mountPaths = new Map(
        service.volumeMounts.map((mount) => [
          mount.volumeId,
          mount.containerPath,
        ])
      );
      canvasService.volumes = (state.volumes[serviceID] ?? []).map(
        (volume) => ({
          containerPath: mountPaths.get(volume.id),
          id: volume.id,
          name: volume.name,
        })
      );
    }
    const references = new Map<string, string[]>();
    for (const [environmentName, value] of Object.entries(
      service.environment
    )) {
      for (const resourceName of referencedResourceNames(value)) {
        const resource = canvas.resources.find(
          (candidate) => candidate.name === resourceName
        );
        if (resource) {
          references.set(resource.id, [
            ...(references.get(resource.id) ?? []),
            environmentName,
          ]);
        }
      }
    }
    canvas.connections = [
      ...canvas.connections.filter(
        (connection) => connection.sourceId !== service.id
      ),
      ...[...references].map(([targetId, environmentNames]) => ({
        environmentNames: environmentNames.toSorted(),
        sourceId: service.id,
        targetId,
      })),
    ];
  }
  return json(service);
};

const handleServiceAction = (
  request: Request,
  state: MockState,
  serviceID: string,
  rest: string[]
): Response | undefined => {
  const [action, ...tail] = rest;
  if (request.method !== "POST" || tail.length > 0 || action !== "redeploy") {
    return undefined;
  }
  const service = state.services[serviceID];
  if (!service) {
    return mockError("not_found", "Service not found", 404);
  }
  service.updatedAt = mockNow();
  return json(service);
};

const handleServiceDeploymentAction = (
  request: Request,
  state: MockState,
  serviceID: string,
  rest: string[]
): Response | undefined => {
  const [resource, deploymentID, action, ...tail] = rest;
  if (
    request.method !== "POST" ||
    resource !== "deployments" ||
    !deploymentID ||
    (action !== "deploy" && action !== "remove" && action !== "restart") ||
    tail.length > 0
  ) {
    return undefined;
  }
  const service = state.services[serviceID];
  const deployments = state.deployments[serviceID] ?? [];
  const deployment = deployments.find(
    (candidate) => candidate.id === deploymentID
  );
  if (!(service && deployment)) {
    return mockError("deployment_not_found", "Deployment not found", 404);
  }
  if (action === "restart") {
    if (service.activeDeploymentId !== deployment.id) {
      return mockError("service_changed", "Deployment is not active", 409);
    }
    service.updatedAt = mockNow();
    return json(service);
  }
  if (action === "deploy") {
    const createdAt = mockNow();
    const next = {
      ...deployment,
      createdAt,
      errorCode: undefined,
      errorMessage: undefined,
      finishedAt: createdAt + 1000,
      id: nextMockID(state, "deployment"),
      status: "succeeded" as const,
    };
    state.deployments[serviceID] = [next, ...deployments];
    service.activeDeploymentId = next.id;
    service.activeConfigHash = next.serviceConfigHash;
    service.activeImageDigest = next.imageDigest;
    service.enabled = true;
    service.updatedAt = createdAt;
    return json(service);
  }
  if (service.activeDeploymentId === deployment.id) {
    service.activeDeploymentId = undefined;
    service.activeConfigHash = undefined;
    service.activeImageDigest = undefined;
    service.enabled = false;
  } else {
    state.deployments[serviceID] = deployments.filter(
      (candidate) => candidate.id !== deployment.id
    );
    const window = state.logs[serviceID];
    if (window) {
      window.records = window.records.filter(
        (record) => record.deploymentId !== deployment.id
      );
    }
  }
  service.updatedAt = mockNow();
  return json(service);
};

const resolvedVariablesResponse = (
  state: MockState,
  serviceID: string
): Response => {
  const service = state.services[serviceID];
  if (!service) {
    return mockError("not_found", "Service not found", 404);
  }
  try {
    return json({
      environment: resolveMockEnvironment(state, serviceID),
    });
  } catch (error) {
    return mockError(
      "variable_resolution_failed",
      error instanceof Error ? error.message : "Unable to resolve variables",
      422
    );
  }
};

const deploymentsResponse = (
  state: MockState,
  serviceID: string,
  deploymentID?: string
): Response => {
  const deployments = state.deployments[serviceID] ?? [];
  if (!deploymentID) {
    return json({ deployments });
  }
  const deployment = deployments.find(
    (candidate) => candidate.id === deploymentID
  );
  return deployment
    ? json(deployment)
    : mockError("deployment_not_found", "Deployment not found", 404);
};

const structuredFieldValue = (
  fields: Record<string, unknown> | undefined,
  path: string
) => {
  let value: unknown = fields;
  for (const segment of path.split(".")) {
    if (typeof value !== "object" || value === null || Array.isArray(value)) {
      return;
    }
    value = (value as Record<string, unknown>)[segment];
  }
  return value;
};

const matchesStructuredFilter = (
  fields: Record<string, unknown> | undefined,
  filter: LogFieldFilter
) => {
  const value = structuredFieldValue(fields, filter.path);
  if (filter.operator === "exists") {
    return value !== undefined;
  }
  const text = value === null ? "null" : String(value ?? "");
  return filter.operator === "equals"
    ? text === filter.value
    : text.toLowerCase().includes(filter.value.toLowerCase());
};

const mockFieldFilters = (value: string | null): LogFieldFilter[] => {
  try {
    const parsed = logFieldFiltersSchema.safeParse(JSON.parse(value ?? "[]"));
    return parsed.success ? parsed.data : [];
  } catch {
    return [];
  }
};

const serviceLogsResponse = (state: MockState, serviceID: string, url: URL) => {
  const window = state.logs[serviceID] ?? { records: [], truncated: false };
  const deploymentID = url.searchParams.get("deploymentId");
  const contains = url.searchParams.get("contains");
  const spanID = url.searchParams.get("spanId");
  const traceID = url.searchParams.get("traceId");
  const fieldFilters = mockFieldFilters(url.searchParams.get("fieldFilters"));
  const cursor = Math.max(
    0,
    Math.trunc(Number(url.searchParams.get("cursor") ?? "0")) || 0
  );
  const limit = Math.max(
    1,
    Math.trunc(Number(url.searchParams.get("limit") ?? "500")) || 500
  );
  const matching = window.records
    .filter((record) => !deploymentID || record.deploymentId === deploymentID)
    .filter((record) => !traceID || record.traceId === traceID)
    .filter((record) => !spanID || record.spanId === spanID)
    .filter(
      (record) =>
        !contains ||
        `${record.text}\n${JSON.stringify(record.fields ?? {})}`
          .toLowerCase()
          .includes(contains.toLowerCase())
    )
    .filter((record) =>
      fieldFilters.every((filter) =>
        matchesStructuredFilter(record.fields, filter)
      )
    );
  const end = Math.max(0, matching.length - cursor);
  const start = Math.max(0, end - limit);
  const records = matching.slice(start, end);
  const nextCursor = start > 0 ? String(cursor + records.length) : undefined;
  return json({ nextCursor, records, truncated: Boolean(nextCursor) });
};

const handleServiceReadModels = (
  request: Request,
  state: MockState,
  serviceID: string,
  rest: string[],
  url: URL
): Response | undefined => {
  const [resource, detail, ...tail] = rest;
  if (request.method !== "GET" || tail.length > 0) {
    return undefined;
  }
  if (resource === "deployments") {
    return deploymentsResponse(state, serviceID, detail);
  }
  if (resource === "previews" && !detail) {
    return json({ previews: state.previews[serviceID] ?? [] });
  }
  if (resource === "variables" && detail === "resolved") {
    return resolvedVariablesResponse(state, serviceID);
  }
  if (resource === "logs" && !detail) {
    return serviceLogsResponse(state, serviceID, url);
  }
  if (resource !== "logs" || detail !== "download") {
    return undefined;
  }
  const lines = (state.logs[serviceID]?.records ?? []).map((record) =>
    JSON.stringify({ type: "record", ...record })
  );
  return new Response(`${lines.join("\n")}\n`, {
    headers: {
      "Content-Disposition": "attachment; filename=platformd-mock-logs.ndjson",
      "Content-Type": "application/x-ndjson",
    },
  });
};

const handleVolumes = async (
  request: Request,
  state: MockState,
  projectID: string,
  serviceID: string,
  rest: string[]
): Promise<Response | undefined> => {
  const [resource, volumeID, ...tail] = rest;
  if (resource !== "volumes" || tail.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !volumeID) {
    return json(state.volumes[serviceID] ?? []);
  }
  if (request.method === "DELETE" && volumeID) {
    state.volumes[serviceID] = (state.volumes[serviceID] ?? []).filter(
      (volume) => volume.id !== volumeID
    );
    const canvasService = state.canvases[projectID]?.resources.find(
      (candidate) => candidate.id === serviceID
    );
    if (canvasService) {
      canvasService.volumes = canvasService.volumes.filter(
        (volume) => volume.id !== volumeID
      );
    }
    return noContent();
  }
  if (request.method !== "POST" || volumeID) {
    return undefined;
  }
  const input = await readObject(request);
  const volume = {
    createdAt: mockNow(),
    id: nextMockID(state, "volume"),
    name: stringField(input, "name", "data"),
    projectId: projectID,
    serviceId: serviceID,
  };
  state.volumes[serviceID] = [...(state.volumes[serviceID] ?? []), volume];
  const canvasService = state.canvases[projectID]?.resources.find(
    (candidate) => candidate.id === serviceID
  );
  canvasService?.volumes.push({ id: volume.id, name: volume.name });
  return json(volume, 201);
};

const handleDomains = async (
  request: Request,
  state: MockState,
  projectID: string,
  serviceID: string,
  rest: string[]
): Promise<Response | undefined> => {
  const [resource, hostname, detail, ...tail] = rest;
  if (resource !== "domains" || tail.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !hostname) {
    return json({ domains: state.domains[serviceID] ?? [] });
  }
  if (request.method === "GET" && hostname && detail === "dns") {
    const exists = (state.domains[serviceID] ?? []).some(
      (domain) => domain.hostname === hostname
    );
    if (!exists) {
      return mockError("domain_not_found", "Domain not found", 404);
    }
    return json({
      status: state.cloudflareDNSSettings.configured ? "ready" : "unmanaged",
    });
  }
  if (detail) {
    return undefined;
  }
  if (request.method === "DELETE" && hostname) {
    state.domains[serviceID] = (state.domains[serviceID] ?? []).filter(
      (domain) => domain.hostname !== hostname
    );
    return noContent();
  }
  if (request.method !== "POST" || hostname) {
    return undefined;
  }
  const input = await readObject(request);
  const hostnameValue = stringField(input, "hostname", "app.mock.local");
  const domain = {
    createdAt: mockNow(),
    hostname: hostnameValue,
    ...mockDomainOutputs(hostnameValue),
    projectId: projectID,
    serviceId: serviceID,
    targetPort: numberField(input, "targetPort", 8080),
  };
  state.domains[serviceID] = [
    ...(state.domains[serviceID] ?? []).filter(
      (current) => current.hostname !== domain.hostname
    ),
    domain,
  ];
  return json(domain, 201);
};

const handleListeners = async (
  request: Request,
  state: MockState,
  projectID: string,
  serviceID: string,
  rest: string[]
): Promise<Response | undefined> => {
  const [resource, protocol, publicPortValue, ...tail] = rest;
  if (resource !== "listeners" || tail.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !protocol) {
    return json({ listeners: state.listeners[serviceID] ?? [] });
  }
  if (request.method === "DELETE" && protocol && publicPortValue) {
    const publicPort = Number(publicPortValue);
    state.listeners[serviceID] = (state.listeners[serviceID] ?? []).filter(
      (listener) =>
        listener.protocol !== protocol || listener.publicPort !== publicPort
    );
    return noContent();
  }
  if (request.method !== "POST" || protocol || publicPortValue) {
    return undefined;
  }
  const input = await readObject(request);
  const listener = {
    createdAt: mockNow(),
    projectId: projectID,
    protocol: stringField(input, "protocol", "tcp") as "tcp" | "udp",
    publicPort: numberField(input, "publicPort", 3000),
    serviceId: serviceID,
    targetPort: numberField(input, "targetPort", 8080),
  };
  const hostId = state.services[serviceID]?.hostId ?? "";
  const conflict = Object.values(state.listeners)
    .flat()
    .find(
      (current) =>
        current.protocol === listener.protocol &&
        current.publicPort === listener.publicPort &&
        current.serviceId !== serviceID &&
        (state.services[current.serviceId]?.hostId ?? "") === hostId
    );
  if (conflict) {
    return json(
      {
        error: {
          code: "listener_conflict",
          listener: conflict,
          message: "Public listener belongs to another service",
        },
      },
      409
    );
  }
  state.listeners[serviceID] = [
    ...(state.listeners[serviceID] ?? []).filter(
      (current) =>
        current.protocol !== listener.protocol ||
        current.publicPort !== listener.publicPort
    ),
    listener,
  ];
  return json(listener, 201);
};

export const handleServicesAPI = async (
  request: Request,
  state: MockState,
  segments: string[],
  url: URL
): Promise<Response | undefined> => {
  const [root, projectID, collection, serviceID, ...rest] = segments;
  if (
    root !== "projects" ||
    !projectID ||
    collection !== "services" ||
    !serviceID
  ) {
    return undefined;
  }
  return (
    (await handleServiceTelemetry(
      request,
      state,
      projectID,
      serviceID,
      rest
    )) ??
    (await handleService(request, state, serviceID, rest)) ??
    handleServiceAction(request, state, serviceID, rest) ??
    handleServiceDeploymentAction(request, state, serviceID, rest) ??
    handleServiceReadModels(request, state, serviceID, rest, url) ??
    (await handleVolumes(request, state, projectID, serviceID, rest)) ??
    (await handleDomains(request, state, projectID, serviceID, rest)) ??
    (await handleListeners(request, state, projectID, serviceID, rest))
  );
};
