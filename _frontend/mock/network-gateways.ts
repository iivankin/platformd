import type { NetworkGateway } from "../web/api";
import {
  apiSegments,
  json,
  mockError,
  numberField,
  readObject,
  stringField,
} from "./http";
import { touchProject } from "./project-helpers";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

const gatewayFromInput = (
  state: MockState,
  projectID: string,
  input: Record<string, unknown>,
  current?: NetworkGateway
): NetworkGateway => {
  const project = state.projects.find(
    (candidate) => candidate.id === projectID
  );
  const mode =
    stringField(input, "mode", "import") === "export" ? "export" : "import";
  const transport =
    stringField(input, "transport", "vpc") === "mesh" ? "mesh" : "vpc";
  const protocol =
    stringField(input, "protocol", "tcp") === "udp" ? "udp" : "tcp";
  const name = stringField(input, "name", "network-gateway");
  const timestamp = mockNow();
  return {
    createdAt: current?.createdAt ?? timestamp,
    id: current?.id ?? nextMockID(state, "network-gateway"),
    interfaceName:
      transport === "mesh" ? "" : stringField(input, "interfaceName", "wg-vpc"),
    internalHostname:
      mode === "import"
        ? `${name}.${project?.name ?? "project"}.internal`
        : undefined,
    listenPort: numberField(input, "listenPort", 5432),
    mode,
    name,
    projectId: projectID,
    projectName: project?.name ?? "project",
    protocol,
    remoteHost: stringField(input, "remoteHost"),
    remotePort: numberField(input, "remotePort", 0),
    sourceAddress:
      transport === "mesh"
        ? ""
        : stringField(input, "sourceAddress", "10.24.0.10"),
    targetPort: numberField(input, "targetPort", 0),
    targetService: state.services[stringField(input, "targetServiceId")]?.name,
    targetServiceId: stringField(input, "targetServiceId"),
    transport,
    updatedAt: timestamp,
  };
};

const canvasResource = (gateway: NetworkGateway) => ({
  enabled: true,
  gatewayListenPort: gateway.listenPort,
  gatewayMode: gateway.mode,
  gatewayProtocol: gateway.protocol,
  gatewayRemoteHost: gateway.remoteHost || undefined,
  gatewayRemotePort: gateway.remotePort || undefined,
  gatewaySourceAddress: gateway.sourceAddress,
  gatewayTargetPort: gateway.targetPort || undefined,
  gatewayTargetServiceId: gateway.targetServiceId || undefined,
  gatewayTransport: gateway.transport,
  id: gateway.id,
  internalHostname:
    gateway.internalHostname ??
    `${gateway.sourceAddress}:${gateway.listenPort}`,
  kind: "network_gateway" as const,
  name: gateway.name,
  status: "running" as const,
  volumes: [],
});

const replaceGatewayConnection = (
  state: MockState,
  projectID: string,
  gateway: NetworkGateway
) => {
  const canvas = state.canvases[projectID];
  if (!canvas) {
    return;
  }
  canvas.connections = canvas.connections.filter(
    (connection) => connection.sourceId !== gateway.id
  );
  if (gateway.mode === "export" && gateway.targetServiceId) {
    canvas.connections.push({
      environmentNames: [],
      sourceId: gateway.id,
      targetId: gateway.targetServiceId,
    });
  }
};

const handleCollection = async (
  request: Request,
  state: MockState,
  projectID: string
) => {
  const canvas = state.canvases[projectID];
  if (request.method === "GET") {
    return json(
      Object.values(state.networkGateways).filter(
        (gateway) => gateway.projectId === projectID
      )
    );
  }
  if (request.method !== "POST") {
    return;
  }
  const gateway = gatewayFromInput(state, projectID, await readObject(request));
  state.networkGateways[gateway.id] = gateway;
  canvas?.resources.push(canvasResource(gateway));
  replaceGatewayConnection(state, projectID, gateway);
  touchProject(state, projectID, "networkGatewayCount");
  return json(gateway, 201);
};

const handleItem = async (
  request: Request,
  state: MockState,
  projectID: string,
  gateway: NetworkGateway
) => {
  const canvas = state.canvases[projectID];
  if (request.method === "GET") {
    return json(gateway);
  }
  if (request.method === "PUT") {
    const updated = gatewayFromInput(
      state,
      projectID,
      await readObject(request),
      gateway
    );
    state.networkGateways[gateway.id] = updated;
    if (canvas) {
      canvas.resources = canvas.resources.map((resource) =>
        resource.id === gateway.id ? canvasResource(updated) : resource
      );
    }
    replaceGatewayConnection(state, projectID, updated);
    return json(updated);
  }
  if (request.method !== "DELETE") {
    return;
  }
  Reflect.deleteProperty(state.networkGateways, gateway.id);
  if (canvas) {
    canvas.resources = canvas.resources.filter(
      (resource) => resource.id !== gateway.id
    );
    canvas.connections = canvas.connections.filter(
      (connection) =>
        connection.sourceId !== gateway.id && connection.targetId !== gateway.id
    );
    canvas.project.networkGatewayCount -= 1;
  }
  touchProject(state, projectID);
  return new Response(null, { status: 204 });
};

export const handleNetworkGatewaysAPI = (
  request: Request,
  state: MockState,
  pathname: string
): Promise<Response | undefined> | Response | undefined => {
  const segments = apiSegments(pathname);
  if (request.method === "GET" && segments.join("/") === "network/addresses") {
    return json({
      addresses: [
        { address: "10.24.0.10", interface: "wg-vpc" },
        { address: "100.64.0.10", interface: "tailscale0" },
      ],
    });
  }
  const [root, projectID, collection, gatewayID, ...rest] = segments;
  if (
    root !== "projects" ||
    !projectID ||
    collection !== "network-gateways" ||
    rest.length > 0
  ) {
    return undefined;
  }
  const canvas = state.canvases[projectID];
  if (!canvas) {
    return mockError("not_found", "Project not found", 404);
  }
  if (!gatewayID) {
    return handleCollection(request, state, projectID);
  }
  const gateway = state.networkGateways[gatewayID ?? ""];
  if (!gateway || gateway.projectId !== projectID) {
    return mockError("not_found", "Network gateway not found", 404);
  }
  return handleItem(request, state, projectID, gateway);
};
