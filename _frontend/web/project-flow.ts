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
  name: string;
  status: ProjectCanvas["resources"][number]["status"];
  statusMessage?: string;
}

export type ResourceFlowNode = Node<ResourceNodeData, "resource">;
export type ResourceFlowEdge = Edge<Record<string, never>, "smoothstep">;

const columnWidth = 320;
const rowHeight = 156;
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
      position: existing.position,
      selected: existing.selected,
    };
  });
};

export const projectFlowElements = (
  canvas: ProjectCanvas
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

  const rowsByColumn = new Map<number, number>();
  const nodes = canvas.resources.map((resource) => {
    const column = columns.get(resource.id) ?? 0;
    const row = rowsByColumn.get(column) ?? 0;
    rowsByColumn.set(column, row + 1);
    return {
      data: {
        activeDeploymentId: resource.activeDeploymentId,
        bucketName: resource.bucketName,
        enabled: resource.enabled,
        imageDigest: resource.imageDigest,
        imageReference: resource.imageReference,
        internalHostname: resource.internalHostname,
        kind: resource.kind,
        name: resource.name,
        status: resource.status,
        statusMessage: resource.statusMessage,
      },
      id: resource.id,
      position: {
        x: originX + column * columnWidth,
        y: originY + row * rowHeight,
      },
      type: "resource" as const,
    };
  });

  const edges = canvas.connections.map((connection) => ({
    data: {},
    id: `${connection.sourceId}:${connection.targetId}`,
    label: connection.environmentNames.join(", "),
    markerEnd: { height: 14, type: MarkerType.ArrowClosed, width: 14 },
    source: connection.sourceId,
    target: connection.targetId,
    type: "smoothstep" as const,
  }));
  return { edges, nodes };
};
