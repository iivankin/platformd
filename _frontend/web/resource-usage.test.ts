import { expect, test } from "bun:test";

import type { ResourceUsage } from "@/api";
import { cpuMillicoresBetween } from "@/resource-usage";

const sample = (
  observedAt: number,
  cpuUsageMicros: number,
  running = true
): ResourceUsage => ({
  cpuUsageMicros,
  hostCpuCores: 8,
  hostMemoryBytes: 16 * 1024 ** 3,
  memoryBytes: 64 * 1024 ** 2,
  observedAt,
  running,
});

test("derives instantaneous CPU millicores from cumulative cgroup counters", () => {
  expect(
    cpuMillicoresBetween(sample(1000, 10_000), sample(6000, 12_510_000))
  ).toBe(2500);
  expect(
    cpuMillicoresBetween(sample(6000, 12_510_000), sample(1000, 10_000))
  ).toBeUndefined();
  expect(
    cpuMillicoresBetween(sample(1000, 10_000, false), sample(6000, 20_000))
  ).toBeUndefined();
});
