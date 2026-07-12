import { expect, test } from "bun:test";

import type { ProjectCanvas } from "@/api";
import { mergeResourceNodeData, projectFlowElements } from "@/project-flow";
import type { ResourceFlowNode } from "@/project-flow";

test("builds deterministic canvas nodes and labeled environment edges", () => {
  const canvas: ProjectCanvas = {
    connections: [
      {
        environmentNames: ["DATABASE_URL", "READ_DATABASE_URL"],
        sourceId: "api",
        targetId: "database",
      },
    ],
    project: {
      createdAt: 1,
      id: "project",
      name: "shop",
      objectStoreCount: 0,
      postgresCount: 1,
      redisCount: 0,
      serviceCount: 1,
      updatedAt: 1,
    },
    resources: [
      {
        enabled: true,
        id: "api",
        imageReference: "example/api:latest",
        internalHostname: "api.shop.internal",
        kind: "service",
        name: "api",
        status: "running",
      },
      {
        enabled: true,
        id: "database",
        imageReference: "17",
        internalHostname: "database.shop.internal",
        kind: "postgres",
        name: "database",
        status: "pending",
      },
    ],
  };
  const flow = projectFlowElements(canvas);
  expect(flow.nodes.map((node) => [node.id, node.position.x])).toEqual([
    ["api", 72],
    ["database", 392],
  ]);
  expect(flow.edges[0]?.label).toBe("DATABASE_URL, READ_DATABASE_URL");
});

test("refreshes status data without resetting dragged node positions", () => {
  const current: ResourceFlowNode[] = [
    {
      data: {
        enabled: true,
        internalHostname: "api.shop.internal",
        kind: "service" as const,
        name: "api",
        status: "pending" as const,
      },
      id: "api",
      position: { x: 900, y: 400 },
      selected: true,
      type: "resource" as const,
    },
  ];
  const [currentNode] = current;
  if (!currentNode) {
    throw new Error("test node is missing");
  }
  const incoming: ResourceFlowNode[] = [
    {
      ...currentNode,
      data: { ...currentNode.data, status: "running" },
      position: { x: 72, y: 56 },
      selected: false,
    },
  ];
  const merged = mergeResourceNodeData(current, incoming);
  expect(merged[0]?.data.status).toBe("running");
  expect(merged[0]?.position).toEqual({ x: 900, y: 400 });
  expect(merged[0]?.selected).toBe(true);
});
