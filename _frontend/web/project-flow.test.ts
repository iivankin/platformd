import { expect, test } from "bun:test";

import type { ProjectCanvas } from "@/api";
import {
  demoCanvasPresets,
  projectCanvasForDemoPreset,
} from "@/project-canvas-demo";
import type { DemoCanvasPreset } from "@/project-canvas-demo";
import { mergeResourceNodeData, projectFlowElements } from "@/project-flow";
import type { ResourceFlowEdge, ResourceFlowNode } from "@/project-flow";

const project: ProjectCanvas["project"] = {
  createdAt: 1,
  id: "project",
  name: "shop",
  networkGatewayCount: 0,
  objectStoreCount: 0,
  postgresCount: 0,
  redisCount: 0,
  serviceCount: 0,
  updatedAt: 1,
};

const emptyCanvas: ProjectCanvas = { connections: [], project, resources: [] };

const resource = (
  id: string,
  kind: ProjectCanvas["resources"][number]["kind"] = "service"
): ProjectCanvas["resources"][number] => ({
  enabled: true,
  id,
  internalHostname: `${id}.shop.internal`,
  kind,
  name: id,
  status: "running",
  volumes: [],
});

test("builds deterministic canvas nodes and routed reference edges", async () => {
  const canvas: ProjectCanvas = {
    connections: [
      {
        environmentNames: ["DATABASE_URL", "READ_DATABASE_URL"],
        sourceId: "api",
        targetId: "database",
      },
    ],
    project: { ...project, postgresCount: 1, serviceCount: 1 },
    resources: [resource("api"), resource("database", "postgres")],
  };

  const flow = await projectFlowElements(canvas);
  expect(flow.nodes.map((node) => [node.id, node.position.x])).toEqual([
    ["api", 72],
    ["database", 472],
  ]);
  expect(flow.nodes[0]?.data).toMatchObject({
    hasIncomingConnection: false,
    hasOutgoingConnection: true,
    layoutHeight: 116,
  });
  expect(flow.nodes[1]?.data).toMatchObject({
    hasIncomingConnection: true,
    hasOutgoingConnection: false,
  });
  expect(flow.nodes.every((node) => node.selectable === false)).toBe(true);
  expect(flow.edges[0]?.ariaLabel).toBe("api connects to database");
  expect(flow.edges[0]?.label).toBeUndefined();
  expect(flow.edges[0]?.sourceHandle).toBe("source:api:database");
  expect(flow.edges[0]?.targetHandle).toBe("target:api:database");
  expect(flow.edges[0]?.type).toBe("resourceConnection");
  expect(flow.edges[0]?.data?.points).toEqual([
    { x: 328, y: 114 },
    { x: 472, y: 114 },
  ]);
});

test("assigns a separate pair of ports to every connection", async () => {
  const canvas: ProjectCanvas = {
    connections: [
      { environmentNames: ["A_URL"], sourceId: "api", targetId: "a" },
      { environmentNames: ["B_URL"], sourceId: "api", targetId: "b" },
      { environmentNames: ["B_URL"], sourceId: "worker", targetId: "b" },
    ],
    project: { ...project, postgresCount: 3, serviceCount: 2 },
    resources: [
      resource("api"),
      resource("worker"),
      resource("a", "postgres"),
      resource("b", "postgres"),
      resource("c", "postgres"),
    ],
  };

  const flow = await projectFlowElements(canvas);
  const api = flow.nodes.find((node) => node.id === "api");
  expect([
    ...(api?.data.leftHandles ?? []),
    ...(api?.data.rightHandles ?? []),
  ]).toEqual(
    expect.arrayContaining([
      { id: "source:api:a", type: "source" },
      { id: "source:api:b", type: "source" },
    ])
  );
  expect(flow.nodes.find((node) => node.id === "b")?.data.leftHandles).toEqual(
    expect.arrayContaining([
      { id: "target:api:b", type: "target" },
      { id: "target:worker:b", type: "target" },
    ])
  );
  expect(new Set(flow.edges.map((edge) => edge.sourceHandle)).size).toBe(3);
  expect(new Set(flow.edges.map((edge) => edge.targetHandle)).size).toBe(3);
  expect(flow.edges.every((edge) => edge.data?.points.length)).toBe(true);
});

interface Segment {
  axis: "horizontal" | "vertical";
  edgeID: string;
  end: number;
  fixed: number;
  start: number;
}

const edgeSegments = (edge: ResourceFlowEdge): Segment[] => {
  const points = edge.data?.points ?? [];
  return points.slice(1).map((point, index) => {
    const previous = points[index];
    if (!previous) {
      throw new Error(`Missing previous point for ${edge.id}`);
    }
    if (previous.y === point.y) {
      return {
        axis: "horizontal",
        edgeID: edge.id,
        end: Math.max(previous.x, point.x),
        fixed: point.y,
        start: Math.min(previous.x, point.x),
      };
    }
    if (previous.x === point.x) {
      return {
        axis: "vertical",
        edgeID: edge.id,
        end: Math.max(previous.y, point.y),
        fixed: point.x,
        start: Math.min(previous.y, point.y),
      };
    }
    throw new Error(`Non-orthogonal segment in ${edge.id}`);
  });
};

const expectNoSharedSegments = (edges: ResourceFlowEdge[]) => {
  const segments = edges.flatMap(edgeSegments);
  for (const [index, left] of segments.entries()) {
    for (const right of segments.slice(index + 1)) {
      if (
        left.edgeID !== right.edgeID &&
        left.axis === right.axis &&
        left.fixed === right.fixed
      ) {
        expect(
          Math.min(left.end, right.end) - Math.max(left.start, right.start)
        ).toBeLessThanOrEqual(0);
      }
    }
  }
};

test("routes every demo preset orthogonally without merged line segments", async () => {
  const presets: Exclude<DemoCanvasPreset, "default">[] =
    demoCanvasPresets.flatMap((preset) =>
      preset.value === "default" ? [] : [preset.value]
    );
  const flows = await Promise.all(
    presets.map((preset) =>
      projectFlowElements(projectCanvasForDemoPreset(emptyCanvas, preset))
    )
  );
  for (const flow of flows) {
    expectNoSharedSegments(flow.edges);
  }
});

test("keeps fan-out and diamond layouts visually centered", async () => {
  const fanOut = await projectFlowElements(
    projectCanvasForDemoPreset(emptyCanvas, "fan-out")
  );
  const fanOutPositions = new Map(
    fanOut.nodes.map((node) => [node.id, node.position])
  );
  expect(fanOutPositions.get("demo-api")?.x).toBe(472);
  expect(fanOutPositions.get("demo-api")?.y).toBe(284);
  expect(fanOutPositions.get("demo-assets")?.x).toBeLessThan(
    fanOutPositions.get("demo-api")?.x ?? 0
  );
  expect(fanOutPositions.get("demo-primary")?.x).toBeGreaterThan(
    fanOutPositions.get("demo-api")?.x ?? 0
  );

  const diamond = await projectFlowElements(
    projectCanvasForDemoPreset(emptyCanvas, "diamond")
  );
  const diamondPositions = new Map(
    diamond.nodes.map((node) => [node.id, node.position])
  );
  expect(diamondPositions.get("demo-web")?.y).toBe(132);
  expect(diamondPositions.get("demo-primary")?.y).toBe(132);
  expect(
    ((diamondPositions.get("demo-api")?.y ?? 0) +
      (diamondPositions.get("demo-worker")?.y ?? 0)) /
      2
  ).toBe(132);
});

test("surrounds marketplace services with dedicated stores and shared hubs", async () => {
  const flow = await projectFlowElements(
    projectCanvasForDemoPreset(emptyCanvas, "marketplace")
  );
  const positions = new Map(flow.nodes.map((node) => [node.id, node.position]));
  const serviceX = positions.get("demo-console")?.x ?? 0;
  expect(positions.get("demo-media")?.x).toBeLessThan(serviceX);
  expect(positions.get("demo-documents")?.x).toBeLessThan(serviceX);
  expect(positions.get("demo-primary")?.x).toBeGreaterThan(serviceX);
  expect(positions.get("demo-cache")?.x).toBeGreaterThan(serviceX);
  expect(positions.get("demo-console")?.y).toBe(positions.get("demo-media")?.y);
  expect(positions.get("demo-crawler")?.y).toBe(
    positions.get("demo-documents")?.y
  );
  expect(positions.get("demo-console")?.y).toBe(
    positions.get("demo-primary")?.y
  );
  expect(positions.get("demo-crawler")?.y).toBe(positions.get("demo-cache")?.y);

  expect(
    flow.nodes.find((node) => node.id === "demo-console")?.data.leftHandles
  ).toEqual([{ id: "source:demo-console:demo-media", type: "source" }]);
  expect(
    flow.nodes.find((node) => node.id === "demo-media")?.data.rightHandles
  ).toEqual([{ id: "target:demo-console:demo-media", type: "target" }]);
  expect(
    flow.nodes
      .find((node) => node.id === "demo-primary")
      ?.data.leftHandles?.map((handle) => handle.id)
  ).toEqual([
    "target:demo-notify-worker:demo-primary",
    "target:demo-console:demo-primary",
    "target:demo-crawler:demo-primary",
  ]);
});

test("parks isolated resources in a row above the connected cluster", async () => {
  const flow = await projectFlowElements(
    projectCanvasForDemoPreset(emptyCanvas, "marketplace")
  );
  const isolatedIDs = new Set(["demo-help", "demo-marketing"]);
  const isolates = flow.nodes
    .filter((node) => isolatedIDs.has(node.id))
    .toSorted((left, right) => left.position.x - right.position.x);
  const connectedTop = Math.min(
    ...flow.nodes
      .filter((node) => !isolatedIDs.has(node.id))
      .map((node) => node.position.y)
  );
  expect(isolates).toHaveLength(2);
  const [left, right] = isolates;
  expect(left?.position.y).toBe(right?.position.y);
  expect(
    connectedTop - ((left?.position.y ?? 0) + (left?.data.layoutHeight ?? 0))
  ).toBe(36);
  expect((right?.position.x ?? 0) - (left?.position.x ?? 0)).toBe(292);
});

test("refreshes status data without resetting dragged node positions", () => {
  const current: ResourceFlowNode[] = [
    {
      data: {
        enabled: true,
        internalHostname: "api.shop.internal",
        kind: "service",
        name: "api",
        status: "pending",
        volumes: [],
      },
      id: "api",
      position: { x: 900, y: 400 },
      selected: true,
      type: "resource",
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
      selectable: false,
      selected: false,
    },
  ];
  const merged = mergeResourceNodeData(current, incoming);
  expect(merged[0]?.data.status).toBe("running");
  expect(merged[0]?.position).toEqual({ x: 900, y: 400 });
  expect(merged[0]?.selected).toBe(false);
});

test("reserves the rendered node height for pending changes and volumes", async () => {
  const canvas: ProjectCanvas = {
    connections: [],
    project: { ...project, objectStoreCount: 1, serviceCount: 1 },
    resources: [resource("api"), resource("assets", "object_store")],
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

  const flow = await projectFlowElements(canvas, overlays);
  expect(flow.nodes[0]?.data).toMatchObject({
    hasIncomingConnection: false,
    hasOutgoingConnection: false,
    layoutHeight: 183,
    pendingChangeCount: 2,
  });
  expect(flow.nodes[0]?.data.volumes).toHaveLength(1);
});

test("centers a service between its dependency resources", async () => {
  const canvas: ProjectCanvas = {
    connections: ["a", "b", "c"].map((targetId) => ({
      environmentNames: [`${targetId.toUpperCase()}_URL`],
      sourceId: "api",
      targetId,
    })),
    project: { ...project, postgresCount: 3, serviceCount: 1 },
    resources: [
      resource("api"),
      resource("a", "postgres"),
      resource("b", "postgres"),
      resource("c", "postgres"),
    ],
  };

  const flow = await projectFlowElements(canvas);
  const positions = new Map(flow.nodes.map((node) => [node.id, node.position]));
  const apiX = positions.get("api")?.x ?? 0;
  expect(apiX).toBe(472);
  expect(positions.get("a")?.x).toBeLessThan(apiX);
  expect(positions.get("b")?.x).toBeGreaterThan(apiX);
  expect(positions.get("c")?.x).toBeGreaterThan(apiX);
  expect(positions.get("api")?.y).toBe(positions.get("a")?.y);
  const api = flow.nodes.find((node) => node.id === "api");
  expect(api?.data.leftHandles).toEqual([
    { id: "source:api:a", type: "source" },
  ]);
  expect(api?.data.rightHandles?.map((handle) => handle.id).toSorted()).toEqual(
    ["source:api:b", "source:api:c"]
  );
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
