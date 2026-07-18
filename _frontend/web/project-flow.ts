import { MarkerType } from "@xyflow/react";
import type { Edge, Node } from "@xyflow/react";

import type { ProjectCanvas } from "@/api";

export interface ResourceNodeData extends Record<string, unknown> {
  activeDeploymentId?: string;
  bucketName?: string;
  enabled: boolean;
  imageDigest?: string;
  imageReference?: string;
  internalHostname: string;
  kind: ProjectCanvas["resources"][number]["kind"];
  layoutX?: number;
  layoutY?: number;
  name: string;
  pendingChangeCount?: number;
  status: ProjectCanvas["resources"][number]["status"];
  statusMessage?: string;
  volumes: ProjectCanvas["resources"][number]["volumes"];
}

export type ResourceFlowNode = Node<ResourceNodeData, "resource">;
export type ResourceFlowEdge = Edge<Record<string, never>, "smoothstep">;
export interface ResourceNodeOverlay {
  pendingChangeCount: number;
  volumes: ResourceNodeData["volumes"];
}

const columnWidth = 320;
const nodeBaseHeight = 116;
const nodeGap = 36;
const pendingChangeRowHeight = 34;
const volumeRowHeight = 33;
const originX = 72;
const originY = 56;

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
      selected: existing.selected,
    };
  });
};

export const projectFlowElements = (
  canvas: ProjectCanvas,
  overlays: ReadonlyMap<string, ResourceNodeOverlay> = new Map()
): { edges: ResourceFlowEdge[]; nodes: ResourceFlowNode[] } => {
  const resourceIDs = new Set(canvas.resources.map((resource) => resource.id));
  const adjacency = new Map<string, string[]>();
  const indegree = new Map<string, number>(
    canvas.resources.map((resource) => [resource.id, 0] as const)
  );
  for (const connection of canvas.connections) {
    if (
      !resourceIDs.has(connection.sourceId) ||
      !resourceIDs.has(connection.targetId)
    ) {
      continue;
    }
    const outgoing = adjacency.get(connection.sourceId) ?? [];
    outgoing.push(connection.targetId);
    adjacency.set(connection.sourceId, outgoing);
    indegree.set(
      connection.targetId,
      (indegree.get(connection.targetId) ?? 0) + 1
    );
  }

  const columns = new Map<string, number>(
    canvas.resources.map((resource) => [resource.id, 0])
  );
  const queue = canvas.resources
    .filter((resource) => indegree.get(resource.id) === 0)
    .map((resource) => resource.id);
  for (const sourceID of queue) {
    for (const targetID of adjacency.get(sourceID) ?? []) {
      columns.set(
        targetID,
        Math.max(columns.get(targetID) ?? 0, (columns.get(sourceID) ?? 0) + 1)
      );
      const remaining = (indegree.get(targetID) ?? 0) - 1;
      indegree.set(targetID, remaining);
      if (remaining === 0) {
        queue.push(targetID);
      }
    }
  }

  const nextYByColumn = new Map<number, number>();
  const nodes = canvas.resources.map((resource) => {
    const column = columns.get(resource.id) ?? 0;
    const y = nextYByColumn.get(column) ?? originY;
    const overlay = overlays.get(resource.id);
    const pendingChangeCount = overlay?.pendingChangeCount ?? 0;
    const volumes = overlay?.volumes ?? resource.volumes;
    nextYByColumn.set(
      column,
      y +
        nodeBaseHeight +
        volumes.length * volumeRowHeight +
        (pendingChangeCount > 0 ? pendingChangeRowHeight : 0) +
        nodeGap
    );
    const x = originX + column * columnWidth;
    return {
      data: {
        activeDeploymentId: resource.activeDeploymentId,
        bucketName: resource.bucketName,
        enabled: resource.enabled,
        imageDigest: resource.imageDigest,
        imageReference: resource.imageReference,
        internalHostname: resource.internalHostname,
        kind: resource.kind,
        layoutX: x,
        layoutY: y,
        name: resource.name,
        pendingChangeCount,
        status: resource.status,
        statusMessage: resource.statusMessage,
        volumes,
      },
      id: resource.id,
      position: {
        x,
        y,
      },
      type: "resource" as const,
    };
  });

  const edges = canvas.connections.map((connection) => ({
    data: {},
    id: `${connection.sourceId}:${connection.targetId}`,
    markerEnd: { height: 14, type: MarkerType.ArrowClosed, width: 14 },
    source: connection.sourceId,
    target: connection.targetId,
    type: "smoothstep" as const,
  }));
  return { edges, nodes };
};
