import { expect, test } from "bun:test";

import type { ResourceUsage } from "@/api";
import {
  cpuMillicoresBetween,
  networkBytesPerSecondBetween,
} from "@/resource-usage";

const sample = (
  observedAt: number,
  cpuUsageMicros: number,
  running = true
): ResourceUsage => ({
  cpuUsageMicros,
  hostCpuCores: 8,
  hostMemoryBytes: 16 * 1024 ** 3,
  memoryBytes: 64 * 1024 ** 2,
  networkAvailable: true,
  networkRxBytes: cpuUsageMicros * 2,
  networkTxBytes: cpuUsageMicros,
  observedAt,
  running,
});

test("derives network ingress and egress rates from cumulative counters", () => {
  expect(
    networkBytesPerSecondBetween(sample(1000, 10_000), sample(6000, 20_000))
  ).toEqual({ egress: 2000, ingress: 4000 });
  expect(
    networkBytesPerSecondBetween(sample(6000, 20_000), sample(1000, 10_000))
  ).toBeUndefined();
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
