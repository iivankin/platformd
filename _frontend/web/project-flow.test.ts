import { expect, test } from "bun:test";

import type { ProjectCanvas } from "@/api";
import { projectFlowElements } from "@/project-flow";

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
      },
      {
        enabled: true,
        id: "database",
        imageReference: "17",
        internalHostname: "database.shop.internal",
        kind: "postgres",
        name: "database",
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
