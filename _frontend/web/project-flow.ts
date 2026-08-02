import { MarkerType } from "@xyflow/react";
import type { Edge, Node, XYPosition } from "@xyflow/react";
import ELK from "elkjs/lib/elk-api.js";
import type {
  ElkEdgeSection,
  ElkExtendedEdge,
  ElkNode,
  ElkPort,
} from "elkjs/lib/elk-api.js";
import elkWorkerURL from "elkjs/lib/elk-worker.min.js" with { type: "file" };

import type { ProjectCanvas, ServiceSource } from "@/api";

export interface ResourceNodeData extends Record<string, unknown> {
  activeDeploymentId?: string;
  bucketName?: string;
  draft?: boolean;
  enabled: boolean;
  imageDigest?: string;
  imageReference?: string;
  gatewayListenPort?: number;
  gatewayMode?: "export" | "import";
  gatewayProtocol?: "tcp" | "udp";
  gatewayRemoteHost?: string;
  gatewayRemotePort?: number;
  gatewaySourceAddress?: string;
  gatewayTargetPort?: number;
  gatewayTargetServiceId?: string;
  gatewayTransport?: "mesh" | "vpc";
  hasIncomingConnection?: boolean;
  hasOutgoingConnection?: boolean;
  incomingHandleIDs?: string[];
  outgoingHandleIDs?: string[];
  source?: ServiceSource;
  internalHostname: string;
  kind: ProjectCanvas["resources"][number]["kind"];
  layoutHeight?: number;
  layoutX?: number;
  layoutY?: number;
  name: string;
  pendingChangeCount?: number;
  status: ProjectCanvas["resources"][number]["status"];
  statusMessage?: string;
  volumes: ProjectCanvas["resources"][number]["volumes"];
}

export interface ResourceConnectionData extends Record<string, unknown> {
  points: XYPosition[];
}

export type ResourceFlowNode = Node<ResourceNodeData, "resource">;
export type ResourceFlowEdge = Edge<
  ResourceConnectionData,
  "resourceConnection"
>;

export interface ResourceNodeOverlay {
  pendingChangeCount: number;
  volumes: ResourceNodeData["volumes"];
}

const nodeBaseHeight = 116;
const nodeWidth = 256;
const pendingChangeRowHeight = 34;
const volumeRowHeight = 33;

const elk = new ELK({ workerUrl: elkWorkerURL });

interface ResourceLayoutDetails {
  draft: boolean;
  height: number;
  pendingChangeCount: number;
  volumes: ResourceNodeData["volumes"];
}

const layoutOptions: Record<string, string> = {
  "elk.algorithm": "layered",
  "elk.direction": "RIGHT",
  "elk.edgeRouting": "ORTHOGONAL",
  "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
  "elk.layered.mergeEdges": "false",
  "elk.layered.nodePlacement.strategy": "SIMPLE",
  "elk.layered.spacing.edgeNodeBetweenLayers": "20",
  "elk.layered.spacing.nodeNodeBetweenLayers": "144",
  "elk.layered.unnecessaryBendpoints": "true",
  "elk.padding": "[top=56,left=72,bottom=56,right=72]",
  "elk.randomSeed": "1",
  "elk.separateConnectedComponents": "true",
  "elk.spacing.componentComponent": "96",
  "elk.spacing.edgeEdge": "12",
  "elk.spacing.nodeNode": "36",
};

const connectionID = (
  connection: ProjectCanvas["connections"][number]
): string => `${connection.sourceId}:${connection.targetId}`;

const sourceHandleID = (
  connection: ProjectCanvas["connections"][number]
): string => `source:${connectionID(connection)}`;

const targetHandleID = (
  connection: ProjectCanvas["connections"][number]
): string => `target:${connectionID(connection)}`;

const resourceLayoutDetails = (
  resources: ProjectCanvas["resources"],
  overlays: ReadonlyMap<string, ResourceNodeOverlay>
): Map<string, ResourceLayoutDetails> => {
  const details = new Map<string, ResourceLayoutDetails>();
  for (const resource of resources) {
    const overlay = overlays.get(resource.id);
    const draft = resource.id.startsWith("draft:");
    const pendingChangeCount = overlay?.pendingChangeCount ?? (draft ? 1 : 0);
    const volumes = overlay?.volumes ?? resource.volumes;
    details.set(resource.id, {
      draft,
      height:
        nodeBaseHeight +
        volumes.length * volumeRowHeight +
        (pendingChangeCount > 0 ? pendingChangeRowHeight : 0),
      pendingChangeCount,
      volumes,
    });
  }
  return details;
};

const sortedConnectionsByResource = (
  connections: ProjectCanvas["connections"],
  resourceOrder: ReadonlyMap<string, number>,
  endpoint: "source" | "target"
): Map<string, ProjectCanvas["connections"]> => {
  const result = new Map<string, ProjectCanvas["connections"]>();
  for (const connection of connections) {
    const resourceID =
      endpoint === "source" ? connection.sourceId : connection.targetId;
    const resourceConnections = result.get(resourceID) ?? [];
    resourceConnections.push(connection);
    result.set(resourceID, resourceConnections);
  }
  for (const [resourceID, resourceConnections] of result) {
    result.set(
      resourceID,
      resourceConnections.toSorted((left, right) => {
        const leftID = endpoint === "source" ? left.targetId : left.sourceId;
        const rightID = endpoint === "source" ? right.targetId : right.sourceId;
        return (
          (resourceOrder.get(leftID) ?? 0) -
            (resourceOrder.get(rightID) ?? 0) ||
          connectionID(left).localeCompare(connectionID(right))
        );
      })
    );
  }
  return result;
};

const resourcePorts = (
  connections: ProjectCanvas["connections"],
  height: number,
  side: "EAST" | "WEST"
): ElkPort[] =>
  connections.map((connection, index) => ({
    height: 0,
    id:
      side === "EAST" ? sourceHandleID(connection) : targetHandleID(connection),
    layoutOptions: { "elk.port.side": side },
    width: 0,
    x: side === "EAST" ? nodeWidth : 0,
    y: (height * (index + 1)) / (connections.length + 1),
  }));

const layoutGraph = (
  canvas: ProjectCanvas,
  connections: ProjectCanvas["connections"],
  details: ReadonlyMap<string, ResourceLayoutDetails>,
  incomingConnections: ReadonlyMap<string, ProjectCanvas["connections"]>,
  outgoingConnections: ReadonlyMap<string, ProjectCanvas["connections"]>
): ElkNode => ({
  children: canvas.resources.map((resource) => {
    const height = details.get(resource.id)?.height ?? nodeBaseHeight;
    return {
      height,
      id: resource.id,
      layoutOptions: { "elk.portConstraints": "FIXED_POS" },
      ports: [
        ...resourcePorts(
          incomingConnections.get(resource.id) ?? [],
          height,
          "WEST"
        ),
        ...resourcePorts(
          outgoingConnections.get(resource.id) ?? [],
          height,
          "EAST"
        ),
      ],
      width: nodeWidth,
    };
  }),
  edges: connections.map(
    (connection): ElkExtendedEdge => ({
      id: connectionID(connection),
      sources: [sourceHandleID(connection)],
      targets: [targetHandleID(connection)],
    })
  ),
  id: "project-canvas",
  layoutOptions,
});

const sectionPoints = (section: ElkEdgeSection | undefined): XYPosition[] => {
  if (!section) {
    return [];
  }
  return [
    section.startPoint,
    ...(section.bendPoints ?? []),
    section.endPoint,
  ].map(({ x, y }) => ({ x, y }));
};

export const mergeResourceNodeData = (
  current: ResourceFlowNode[],
  incoming: ResourceFlowNode[]
): ResourceFlowNode[] => {
  if (
    current.length !== incoming.length ||
    incoming.some(
      (node) => !current.some((existing) => existing.id === node.id)
    )
  ) {
    return incoming;
  }
  const currentByID = new Map(current.map((node) => [node.id, node]));
  return incoming.map((node) => {
    const existing = currentByID.get(node.id);
    if (!existing) {
      return node;
    }
    return {
      ...node,
      dragging: existing.dragging,
      position:
        existing.data.layoutX === existing.position.x &&
        existing.data.layoutY === existing.position.y
          ? node.position
          : existing.position,
      selected: node.selectable === false ? false : existing.selected,
    };
  });
};

export const projectFlowElements = async (
  canvas: ProjectCanvas,
  overlays: ReadonlyMap<string, ResourceNodeOverlay> = new Map()
): Promise<{ edges: ResourceFlowEdge[]; nodes: ResourceFlowNode[] }> => {
  const resourceIDs = new Set(canvas.resources.map((resource) => resource.id));
  const validConnections = canvas.connections.filter(
    (connection) =>
      resourceIDs.has(connection.sourceId) &&
      resourceIDs.has(connection.targetId)
  );
  const incomingResourceIDs = new Set(
    validConnections.map((connection) => connection.targetId)
  );
  const outgoingResourceIDs = new Set(
    validConnections.map((connection) => connection.sourceId)
  );
  const resourceOrder = new Map(
    canvas.resources.map((resource, index) => [resource.id, index])
  );
  const details = resourceLayoutDetails(canvas.resources, overlays);
  const incomingConnections = sortedConnectionsByResource(
    validConnections,
    resourceOrder,
    "target"
  );
  const outgoingConnections = sortedConnectionsByResource(
    validConnections,
    resourceOrder,
    "source"
  );
  const laidOutGraph = await elk.layout(
    layoutGraph(
      canvas,
      validConnections,
      details,
      incomingConnections,
      outgoingConnections
    )
  );
  const laidOutNodes = new Map(
    (laidOutGraph.children ?? []).map((node) => [node.id, node])
  );
  const laidOutEdges = new Map(
    (laidOutGraph.edges ?? []).map((edge) => [edge.id, edge])
  );

  const nodes = canvas.resources.map((resource) => {
    const resourceDetails = details.get(resource.id) ?? {
      draft: false,
      height: nodeBaseHeight,
      pendingChangeCount: 0,
      volumes: resource.volumes,
    };
    const layoutNode = laidOutNodes.get(resource.id);
    const position = { x: layoutNode?.x ?? 72, y: layoutNode?.y ?? 56 };
    const incomingHandleIDs = (incomingConnections.get(resource.id) ?? []).map(
      targetHandleID
    );
    const outgoingHandleIDs = (outgoingConnections.get(resource.id) ?? []).map(
      sourceHandleID
    );
    return {
      data: {
        activeDeploymentId: resource.activeDeploymentId,
        bucketName: resource.bucketName,
        draft: resourceDetails.draft,
        enabled: resource.enabled,
        gatewayListenPort: resource.gatewayListenPort,
        gatewayMode: resource.gatewayMode,
        gatewayProtocol: resource.gatewayProtocol,
        gatewayRemoteHost: resource.gatewayRemoteHost,
        gatewayRemotePort: resource.gatewayRemotePort,
        gatewaySourceAddress: resource.gatewaySourceAddress,
        gatewayTargetPort: resource.gatewayTargetPort,
        gatewayTargetServiceId: resource.gatewayTargetServiceId,
        gatewayTransport: resource.gatewayTransport,
        hasIncomingConnection: incomingResourceIDs.has(resource.id),
        hasOutgoingConnection: outgoingResourceIDs.has(resource.id),
        imageDigest: resource.imageDigest,
        imageReference: resource.imageReference,
        incomingHandleIDs,
        internalHostname: resource.internalHostname,
        kind: resource.kind,
        layoutHeight: resourceDetails.height,
        layoutX: position.x,
        layoutY: position.y,
        name: resource.name,
        outgoingHandleIDs,
        pendingChangeCount: resourceDetails.pendingChangeCount,
        source: resource.source,
        status: resource.status,
        statusMessage: resource.statusMessage,
        volumes: resourceDetails.volumes,
      },
      id: resource.id,
      position,
      selectable: false,
      type: "resource" as const,
    };
  });

  const resourceNames = new Map(
    canvas.resources.map((resource) => [resource.id, resource.name])
  );
  const edges = validConnections.map((connection) => {
    const id = connectionID(connection);
    const layoutEdge = laidOutEdges.get(id);
    return {
      ariaLabel: `${resourceNames.get(connection.sourceId) ?? connection.sourceId} connects to ${resourceNames.get(connection.targetId) ?? connection.targetId}`,
      className: "resource-connection",
      data: { points: sectionPoints(layoutEdge?.sections?.[0]) },
      id,
      markerEnd: {
        height: 12,
        type: MarkerType.ArrowClosed,
        width: 12,
      },
      source: connection.sourceId,
      sourceHandle: sourceHandleID(connection),
      target: connection.targetId,
      targetHandle: targetHandleID(connection),
      type: "resourceConnection" as const,
    };
  });
  return { edges, nodes };
};
