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

export interface ResourceSideHandle {
  id: string;
  type: "source" | "target";
}

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
  leftHandles?: ResourceSideHandle[];
  rightHandles?: ResourceSideHandle[];
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

const surroundSinkKinds = new Set<ProjectCanvas["resources"][number]["kind"]>([
  "object_store",
  "postgres",
  "redis",
]);

const elk = new ELK({ workerUrl: elkWorkerURL });

interface ResourceLayoutDetails {
  draft: boolean;
  height: number;
  pendingChangeCount: number;
  volumes: ResourceNodeData["volumes"];
}

type PortConstraints = "FIXED_POS" | "FIXED_SIDE";

interface LayoutPort {
  handleID: string;
  otherResourceID: string;
}

const baseLayoutOptions: Record<string, string> = {
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
  "elk.spacing.edgeEdge": "16",
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

const incomingDegree = (
  resourceID: string,
  connections: ProjectCanvas["connections"]
): number => {
  let count = 0;
  for (const connection of connections) {
    if (connection.targetId === resourceID) {
      count += 1;
    }
  }
  return count;
};

// Dedicated low-degree dependency sinks go left of services; shared hubs stay
// right. Only postgres/redis/object_store participate so service chains keep a
// normal left-to-right layered flow.
const partitionLeftSinks = (
  resources: ProjectCanvas["resources"],
  connections: ProjectCanvas["connections"]
): Set<string> => {
  const hasOutgoing = new Set(
    connections.map((connection) => connection.sourceId)
  );
  const hasIncoming = new Set(
    connections.map((connection) => connection.targetId)
  );
  const sinks = resources
    .filter(
      (resource) =>
        surroundSinkKinds.has(resource.kind) &&
        hasIncoming.has(resource.id) &&
        !hasOutgoing.has(resource.id)
    )
    .toSorted(
      (left, right) =>
        incomingDegree(left.id, connections) -
          incomingDegree(right.id, connections) ||
        left.id.localeCompare(right.id)
    );
  if (sinks.length < 2) {
    return new Set();
  }
  const leftCount = Math.floor(sinks.length / 2);
  return new Set(sinks.slice(0, leftCount).map((resource) => resource.id));
};

const sortLayoutPorts = (
  ports: LayoutPort[],
  resourceOrder: ReadonlyMap<string, number>
): LayoutPort[] =>
  ports.toSorted(
    (left, right) =>
      (resourceOrder.get(left.otherResourceID) ?? 0) -
        (resourceOrder.get(right.otherResourceID) ?? 0) ||
      left.handleID.localeCompare(right.handleID)
  );

const resourceSidePorts = (
  resources: ProjectCanvas["resources"],
  connections: ProjectCanvas["connections"],
  leftSinkIDs: ReadonlySet<string>,
  resourceOrder: ReadonlyMap<string, number>
): Map<string, { east: LayoutPort[]; west: LayoutPort[] }> => {
  const sides = new Map<string, { east: LayoutPort[]; west: LayoutPort[] }>();
  for (const resource of resources) {
    sides.set(resource.id, { east: [], west: [] });
  }
  for (const connection of connections) {
    const sourcePorts = sides.get(connection.sourceId);
    const targetPorts = sides.get(connection.targetId);
    if (!sourcePorts || !targetPorts) {
      continue;
    }
    if (leftSinkIDs.has(connection.targetId)) {
      // Layout edge is reversed (sink → service), so the sink exposes an EAST
      // source-port and the service exposes a WEST target-port to ELK.
      sourcePorts.west.push({
        handleID: sourceHandleID(connection),
        otherResourceID: connection.targetId,
      });
      targetPorts.east.push({
        handleID: targetHandleID(connection),
        otherResourceID: connection.sourceId,
      });
      continue;
    }
    sourcePorts.east.push({
      handleID: sourceHandleID(connection),
      otherResourceID: connection.targetId,
    });
    targetPorts.west.push({
      handleID: targetHandleID(connection),
      otherResourceID: connection.sourceId,
    });
  }
  for (const [resourceID, ports] of sides) {
    sides.set(resourceID, {
      east: sortLayoutPorts(ports.east, resourceOrder),
      west: sortLayoutPorts(ports.west, resourceOrder),
    });
  }
  return sides;
};

const elkPorts = (
  ports: LayoutPort[],
  height: number,
  side: "EAST" | "WEST",
  constraints: PortConstraints
): ElkPort[] =>
  ports.map((port, index) => {
    const elkPort: ElkPort = {
      height: 0,
      id: port.handleID,
      layoutOptions: { "elk.port.side": side },
      width: 0,
    };
    if (constraints === "FIXED_POS") {
      elkPort.x = side === "EAST" ? nodeWidth : 0;
      elkPort.y = (height * (index + 1)) / (ports.length + 1);
    }
    return elkPort;
  });

const layoutOptionsForPass = (
  lockModelOrder: boolean
): Record<string, string> => ({
  ...baseLayoutOptions,
  ...(lockModelOrder
    ? {
        "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
        "elk.layered.crossingMinimization.forceNodeModelOrder": "true",
      }
    : {}),
});

const layoutGraph = (
  resources: ProjectCanvas["resources"],
  connections: ProjectCanvas["connections"],
  details: ReadonlyMap<string, ResourceLayoutDetails>,
  sidePorts: ReadonlyMap<string, { east: LayoutPort[]; west: LayoutPort[] }>,
  resourceOrder: ReadonlyMap<string, number>,
  leftSinkIDs: ReadonlySet<string>,
  portConstraints: PortConstraints,
  lockModelOrder: boolean
): ElkNode => ({
  children: resources
    .toSorted(
      (left, right) =>
        (resourceOrder.get(left.id) ?? 0) - (resourceOrder.get(right.id) ?? 0)
    )
    .map((resource) => {
      const height = details.get(resource.id)?.height ?? nodeBaseHeight;
      const ports = sidePorts.get(resource.id) ?? { east: [], west: [] };
      return {
        height,
        id: resource.id,
        layoutOptions: { "elk.portConstraints": portConstraints },
        ports: [
          ...elkPorts(ports.west, height, "WEST", portConstraints),
          ...elkPorts(ports.east, height, "EAST", portConstraints),
        ],
        width: nodeWidth,
      };
    }),
  edges: connections.map((connection): ElkExtendedEdge => {
    // Reverse left-sink edges so ELK places those dependencies on the left.
    if (leftSinkIDs.has(connection.targetId)) {
      return {
        id: connectionID(connection),
        sources: [targetHandleID(connection)],
        targets: [sourceHandleID(connection)],
      };
    }
    return {
      id: connectionID(connection),
      sources: [sourceHandleID(connection)],
      targets: [targetHandleID(connection)],
    };
  }),
  id: "project-canvas",
  layoutOptions: layoutOptionsForPass(lockModelOrder),
});

const sectionPoints = (section: ElkEdgeSection | undefined): XYPosition[] => {
  if (!section) {
    return [];
  }
  // ELK sometimes emits microscopic float drift on "straight" segments;
  // snap so React Flow paths and orthogonality checks stay exact.
  return [
    section.startPoint,
    ...(section.bendPoints ?? []),
    section.endPoint,
  ].map(({ x, y }) => ({
    x: Math.round(x * 1000) / 1000,
    y: Math.round(y * 1000) / 1000,
  }));
};

const resourceOrderFromLayout = (
  graph: ElkNode,
  fallback: ReadonlyMap<string, number>
): Map<string, number> => {
  const children = [...(graph.children ?? [])].toSorted((left, right) => {
    const yDelta = (left.y ?? 0) - (right.y ?? 0);
    if (yDelta !== 0) {
      return yDelta;
    }
    const xDelta = (left.x ?? 0) - (right.x ?? 0);
    if (xDelta !== 0) {
      return xDelta;
    }
    return (fallback.get(left.id) ?? 0) - (fallback.get(right.id) ?? 0);
  });
  return new Map(children.map((child, index) => [child.id, index]));
};

const sideHandles = (ports: LayoutPort[]): ResourceSideHandle[] =>
  ports.map((port) => ({
    id: port.handleID,
    type: port.handleID.startsWith("source:") ? "source" : "target",
  }));

const runLayout = async (
  resources: ProjectCanvas["resources"],
  connections: ProjectCanvas["connections"],
  details: ReadonlyMap<string, ResourceLayoutDetails>,
  resourceOrder: ReadonlyMap<string, number>,
  leftSinkIDs: ReadonlySet<string>,
  portConstraints: PortConstraints,
  lockModelOrder: boolean
): Promise<{
  graph: ElkNode;
  sidePorts: Map<string, { east: LayoutPort[]; west: LayoutPort[] }>;
}> => {
  const sidePorts = resourceSidePorts(
    resources,
    connections,
    leftSinkIDs,
    resourceOrder
  );
  const graph = await elk.layout(
    layoutGraph(
      resources,
      connections,
      details,
      sidePorts,
      resourceOrder,
      leftSinkIDs,
      portConstraints,
      lockModelOrder
    )
  );
  return { graph, sidePorts };
};

const layoutPaddingTop = 56;
const isolatedComponentGap = 36;

const compactIsolatedPositions = (
  positions: Map<string, XYPosition>,
  heights: ReadonlyMap<string, number>,
  connectedIDs: ReadonlySet<string>,
  isolatedIDs: readonly string[]
): { positions: Map<string, XYPosition>; yShift: number } => {
  if (isolatedIDs.length === 0 || connectedIDs.size === 0) {
    return { positions, yShift: 0 };
  }

  let mainTop = Number.POSITIVE_INFINITY;
  let mainLeft = Number.POSITIVE_INFINITY;
  for (const resourceID of connectedIDs) {
    const position = positions.get(resourceID);
    if (!position) {
      continue;
    }
    mainTop = Math.min(mainTop, position.y);
    mainLeft = Math.min(mainLeft, position.x);
  }
  if (!Number.isFinite(mainTop) || !Number.isFinite(mainLeft)) {
    return { positions, yShift: 0 };
  }

  let maxHeight = nodeBaseHeight;
  for (const resourceID of isolatedIDs) {
    maxHeight = Math.max(maxHeight, heights.get(resourceID) ?? nodeBaseHeight);
  }

  const next = new Map(positions);
  // Park orphans in a horizontal row just above the connected cluster so
  // multiple isolates do not stack into a tall separate-component tower.
  const rowY = mainTop - isolatedComponentGap - maxHeight;
  for (const [index, resourceID] of isolatedIDs.entries()) {
    next.set(resourceID, {
      x: mainLeft + index * (nodeWidth + isolatedComponentGap),
      y: rowY,
    });
  }

  let minY = Number.POSITIVE_INFINITY;
  for (const position of next.values()) {
    minY = Math.min(minY, position.y);
  }
  const yShift = Number.isFinite(minY) ? minY - layoutPaddingTop : 0;
  if (yShift === 0) {
    return { positions: next, yShift: 0 };
  }
  for (const [resourceID, position] of next) {
    next.set(resourceID, { x: position.x, y: position.y - yShift });
  }
  return { positions: next, yShift };
};

const shiftPoints = (points: XYPosition[], yShift: number): XYPosition[] =>
  yShift === 0
    ? points
    : points.map((point) => ({ x: point.x, y: point.y - yShift }));

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
  const leftSinkIDs = partitionLeftSinks(canvas.resources, validConnections);
  const seedOrder = new Map(
    canvas.resources.map((resource, index) => [resource.id, index])
  );
  const details = resourceLayoutDetails(canvas.resources, overlays);
  // Pass 1: let ELK freely order ports while minimizing crossings. Pass 2:
  // lock evenly spaced FIXED_POS ports to that vertical rank so React Flow
  // handles match the routed edge endpoints.
  const exploratory = await runLayout(
    canvas.resources,
    validConnections,
    details,
    seedOrder,
    leftSinkIDs,
    "FIXED_SIDE",
    false
  );
  const layoutOrder = resourceOrderFromLayout(exploratory.graph, seedOrder);
  const { graph: laidOutGraph, sidePorts } = await runLayout(
    canvas.resources,
    validConnections,
    details,
    layoutOrder,
    leftSinkIDs,
    "FIXED_POS",
    true
  );
  const laidOutNodes = new Map(
    (laidOutGraph.children ?? []).map((node) => [node.id, node])
  );
  const laidOutEdges = new Map(
    (laidOutGraph.edges ?? []).map((edge) => [edge.id, edge])
  );
  const connectedResourceIDs = new Set<string>([
    ...incomingResourceIDs,
    ...outgoingResourceIDs,
  ]);
  const isolatedResourceIDs = canvas.resources
    .map((resource) => resource.id)
    .filter((resourceID) => !connectedResourceIDs.has(resourceID))
    .toSorted((left, right) => left.localeCompare(right));
  const rawPositions = new Map(
    canvas.resources.map((resource) => {
      const layoutNode = laidOutNodes.get(resource.id);
      return [
        resource.id,
        { x: layoutNode?.x ?? 72, y: layoutNode?.y ?? layoutPaddingTop },
      ];
    })
  );
  const heights = new Map(
    canvas.resources.map((resource) => [
      resource.id,
      details.get(resource.id)?.height ?? nodeBaseHeight,
    ])
  );
  const { positions, yShift } = compactIsolatedPositions(
    rawPositions,
    heights,
    connectedResourceIDs,
    isolatedResourceIDs
  );

  const nodes = canvas.resources.map((resource) => {
    const resourceDetails = details.get(resource.id) ?? {
      draft: false,
      height: nodeBaseHeight,
      pendingChangeCount: 0,
      volumes: resource.volumes,
    };
    const position = positions.get(resource.id) ?? {
      x: 72,
      y: layoutPaddingTop,
    };
    const ports = sidePorts.get(resource.id) ?? { east: [], west: [] };
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
        internalHostname: resource.internalHostname,
        kind: resource.kind,
        layoutHeight: resourceDetails.height,
        layoutX: position.x,
        layoutY: position.y,
        leftHandles: sideHandles(ports.west),
        name: resource.name,
        pendingChangeCount: resourceDetails.pendingChangeCount,
        rightHandles: sideHandles(ports.east),
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
    const points = sectionPoints(layoutEdge?.sections?.[0]);
    const oriented = leftSinkIDs.has(connection.targetId)
      ? points.toReversed()
      : points;
    return {
      ariaLabel: `${resourceNames.get(connection.sourceId) ?? connection.sourceId} connects to ${resourceNames.get(connection.targetId) ?? connection.targetId}`,
      className: "resource-connection",
      data: {
        // ELK routed left-sink edges sink→service; flip back to semantic
        // service→dependency direction for React Flow arrows.
        points: shiftPoints(oriented, yShift),
      },
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
