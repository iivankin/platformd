import type { Service } from "../web/api";
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
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

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
  if (request.method !== "PUT") {
    return undefined;
  }
  const input = await readObject(request);
  service.enabled = booleanField(input, "enabled", service.enabled);
  service.imageReference = stringField(
    input,
    "imageReference",
    service.imageReference
  );
  service.environment = stringRecord(input.environment);
  service.resourceReferences = Array.isArray(input.resourceReferences)
    ? (input.resourceReferences as Service["resourceReferences"])
    : [];
  service.healthPath = stringField(input, "healthPath") || undefined;
  service.targetPort =
    typeof input.targetPort === "number" ? input.targetPort : undefined;
  service.updatedAt = mockNow();
  const canvas = state.canvases[service.projectId];
  if (canvas) {
    const references = new Map<string, string[]>();
    for (const reference of service.resourceReferences) {
      references.set(reference.resourceId, [
        ...(references.get(reference.resourceId) ?? []),
        reference.environmentName,
      ]);
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
  if (resource === "deployments" && !detail) {
    return json({ deployments: state.deployments[serviceID] ?? [] });
  }
  if (resource === "deployments" && detail) {
    const deployment = (state.deployments[serviceID] ?? []).find(
      (candidate) => candidate.id === detail
    );
    return deployment
      ? json(deployment)
      : mockError("deployment_not_found", "Deployment not found", 404);
  }
  if (resource === "logs" && !detail) {
    const window = state.logs[serviceID] ?? { records: [], truncated: false };
    const deploymentID = url.searchParams.get("deploymentId");
    const contains = url.searchParams.get("contains");
    const limit = Math.max(
      1,
      Math.trunc(Number(url.searchParams.get("limit") ?? "500")) || 500
    );
    const records = window.records
      .filter((record) => !deploymentID || record.deploymentId === deploymentID)
      .filter((record) => !contains || record.text.includes(contains))
      .slice(-limit);
    return json({ records, truncated: window.truncated });
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
  if (request.method === "GET" && volumeID === "owner-suggestion") {
    return json({
      exactNumeric: true,
      imageUser: "1000:1000",
      ownerGid: 1000,
      ownerUid: 1000,
    });
  }
  if (request.method === "DELETE" && volumeID) {
    state.volumes[serviceID] = (state.volumes[serviceID] ?? []).filter(
      (volume) => volume.id !== volumeID
    );
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
    ownerGid: numberField(input, "ownerGid", 1000),
    ownerUid: numberField(input, "ownerUid", 1000),
    projectId: projectID,
    serviceId: serviceID,
  };
  state.volumes[serviceID] = [...(state.volumes[serviceID] ?? []), volume];
  return json(volume, 201);
};

const handleDomains = async (
  request: Request,
  state: MockState,
  projectID: string,
  serviceID: string,
  rest: string[]
): Promise<Response | undefined> => {
  const [resource, hostname, ...tail] = rest;
  if (resource !== "domains" || tail.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !hostname) {
    return json({ domains: state.domains[serviceID] ?? [] });
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
  const domain = {
    createdAt: mockNow(),
    hostname: stringField(input, "hostname", "app.mock.local"),
    projectId: projectID,
    serviceId: serviceID,
  };
  state.domains[serviceID] = [...(state.domains[serviceID] ?? []), domain];
  return json(domain, 201);
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
    (await handleService(request, state, serviceID, rest)) ??
    handleServiceAction(request, state, serviceID, rest) ??
    handleServiceDeploymentAction(request, state, serviceID, rest) ??
    handleServiceReadModels(request, state, serviceID, rest, url) ??
    (await handleVolumes(request, state, projectID, serviceID, rest)) ??
    (await handleDomains(request, state, projectID, serviceID, rest))
  );
};
