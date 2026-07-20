import { expect, test } from "bun:test";

import type { ProjectCanvas } from "@/api";
import { mergeResourceNodeData, projectFlowElements } from "@/project-flow";
import type { ResourceFlowNode } from "@/project-flow";

test("builds deterministic canvas nodes and reference edges", () => {
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
      networkGatewayCount: 0,
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
        volumes: [],
      },
      {
        enabled: true,
        id: "database",
        imageReference: "17",
        internalHostname: "database.shop.internal",
        kind: "postgres",
        name: "database",
        status: "pending",
        volumes: [],
      },
    ],
  };
  const flow = projectFlowElements(canvas);
  expect(flow.nodes.map((node) => [node.id, node.position.x])).toEqual([
    ["api", 72],
    ["database", 392],
  ]);
  expect(flow.nodes[0]?.data).toMatchObject({
    hasIncomingConnection: false,
    hasOutgoingConnection: true,
  });
  expect(flow.nodes[1]?.data).toMatchObject({
    hasIncomingConnection: true,
    hasOutgoingConnection: false,
  });
  expect(flow.edges[0]?.label).toBeUndefined();
  expect(flow.edges[0]?.type).toBe("smoothstep");
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
        volumes: [],
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

test("reserves canvas space for pending changes and volume rows", () => {
  const canvas: ProjectCanvas = {
    connections: [],
    project: {
      createdAt: 1,
      id: "project",
      name: "shop",
      networkGatewayCount: 0,
      objectStoreCount: 1,
      postgresCount: 0,
      redisCount: 0,
      serviceCount: 1,
      updatedAt: 1,
    },
    resources: [
      {
        enabled: true,
        id: "api",
        internalHostname: "api.shop.internal",
        kind: "service",
        name: "api",
        status: "running",
        volumes: [],
      },
      {
        bucketName: "assets",
        enabled: true,
        id: "assets",
        internalHostname: "assets.shop.internal",
        kind: "object_store",
        name: "assets",
        status: "running",
        volumes: [],
      },
    ],
  };
  const overlays = new Map([
    [
      "api",
      {
        pendingChangeCount: 2,
        volumes: [{ id: "volume", name: "data" }],
      },
    ],
  ]);
  const flow = projectFlowElements(canvas, overlays);
  expect(flow.nodes[0]?.data.hasIncomingConnection).toBe(false);
  expect(flow.nodes[0]?.data.hasOutgoingConnection).toBe(false);
  expect(flow.nodes[0]?.data.pendingChangeCount).toBe(2);
  expect(flow.nodes[0]?.data.volumes).toHaveLength(1);
  expect(flow.nodes[1]?.position.y).toBe(275);
});

test("moves layout-managed nodes when their calculated position changes", () => {
  const current: ResourceFlowNode[] = [
    {
      data: {
        enabled: true,
        internalHostname: "assets.shop.internal",
        kind: "object_store",
        layoutX: 72,
        layoutY: 208,
        name: "assets",
        status: "running",
        volumes: [],
      },
      id: "assets",
      position: { x: 72, y: 208 },
      type: "resource",
    },
  ];
  const [currentNode] = current;
  if (!currentNode) {
    throw new Error("current node is missing");
  }
  const incoming: ResourceFlowNode[] = [
    {
      ...currentNode,
      data: { ...currentNode.data, layoutY: 275 },
      position: { x: 72, y: 275 },
    },
  ];
  expect(mergeResourceNodeData(current, incoming)[0]?.position.y).toBe(275);
});
