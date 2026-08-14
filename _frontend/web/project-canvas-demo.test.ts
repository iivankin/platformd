import { expect, test } from "bun:test";

import type { ProjectCanvas } from "@/api";
import {
  demoCanvasPresets,
  projectCanvasForDemoPreset,
} from "@/project-canvas-demo";
import type { DemoCanvasPreset } from "@/project-canvas-demo";

const canvas: ProjectCanvas = {
  connections: [],
  project: {
    createdAt: 1,
    hasIcon: false,
    id: "project-demo",
    name: "storefront",
    networkGatewayCount: 0,
    objectStoreCount: 0,
    postgresCount: 0,
    redisCount: 0,
    serviceCount: 0,
    updatedAt: 1,
  },
  resources: [],
};

test("builds valid connected resources for every complex demo preset", () => {
  const expectedSizes = {
    "data-pipeline": { connections: 10, resources: 10 },
    dense: { connections: 14, resources: 9 },
    diamond: { connections: 4, resources: 4 },
    "fan-out": { connections: 6, resources: 7 },
    marketplace: { connections: 9, resources: 10 },
    microservices: { connections: 13, resources: 9 },
    saas: { connections: 10, resources: 8 },
    "web-app": { connections: 7, resources: 6 },
  } as const;
  const allowDisconnected = new Set<Exclude<DemoCanvasPreset, "default">>([
    "marketplace",
  ]);

  for (const preset of demoCanvasPresets) {
    if (preset.value === "default") {
      continue;
    }
    const result = projectCanvasForDemoPreset(canvas, preset.value);
    const resourceIDs = new Set(
      result.resources.map((resource) => resource.id)
    );
    expect(result.resources).toHaveLength(
      expectedSizes[preset.value].resources
    );
    expect(result.connections).toHaveLength(
      expectedSizes[preset.value].connections
    );
    expect(resourceIDs.size).toBe(result.resources.length);
    const connectedResourceIDs = new Set(
      result.connections.flatMap((connection) => [
        connection.sourceId,
        connection.targetId,
      ])
    );
    if (allowDisconnected.has(preset.value)) {
      expect([...connectedResourceIDs].every((id) => resourceIDs.has(id))).toBe(
        true
      );
      expect(connectedResourceIDs.size).toBeLessThan(resourceIDs.size);
    } else {
      expect(connectedResourceIDs).toEqual(resourceIDs);
    }
    expect(
      result.connections.every(
        (connection) =>
          resourceIDs.has(connection.sourceId) &&
          resourceIDs.has(connection.targetId)
      )
    ).toBe(true);
  }
});
