import { describe, expect, test } from "bun:test";

import {
  metricTimelineFraction,
  nearestMetricPointIndex,
} from "./metric-chart";

describe("metric timeline geometry", () => {
  test("positions sparse samples by their timestamp", () => {
    expect(metricTimelineFraction(1000, 1000, 5000)).toBe(0);
    expect(metricTimelineFraction(2000, 1000, 5000)).toBe(0.25);
    expect(metricTimelineFraction(5000, 1000, 5000)).toBe(1);
  });

  test("selects the timestamp-nearest sparse sample", () => {
    const points = [
      { observedAt: 1000 },
      { observedAt: 2000 },
      { observedAt: 5000 },
    ];
    expect(nearestMetricPointIndex(points, 2400)).toBe(1);
    expect(nearestMetricPointIndex(points, 4000)).toBe(2);
  });
});
