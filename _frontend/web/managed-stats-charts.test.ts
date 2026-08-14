import { describe, expect, test } from "bun:test";

import {
  foldUnselectedOperationsIntoOther,
  operationsBreakdownKeys,
} from "@/managed-stats-charts";

describe("operationsBreakdownKeys", () => {
  test("ranks redis command series by total rate", () => {
    const keys = operationsBreakdownKeys({
      history: {
        from: 1,
        points: [
          {
            metrics: {
              "cmd.get": 10,
              "cmd.set": 4,
              otherCommandsPerSecond: 1,
            },
            observedAt: 1000,
          },
          {
            metrics: {
              "cmd.get": 8,
              "cmd.set": 12,
              otherCommandsPerSecond: 2,
            },
            observedAt: 2000,
          },
        ],
        stepMillis: 1000,
        to: 2000,
        totals: {},
      },
      otherKey: "otherCommandsPerSecond",
      prefix: "cmd.",
    });
    expect(keys.map((item) => ({ key: item.key, label: item.label }))).toEqual([
      { key: "cmd.get", label: "get" },
      { key: "cmd.set", label: "set" },
      { key: "otherCommandsPerSecond", label: "Other" },
    ]);
  });

  test("keeps seven command series plus Other by default", () => {
    const metrics: Record<string, number> = { otherCommandsPerSecond: 1 };
    for (let index = 0; index < 10; index += 1) {
      metrics[`cmd.cmd${index}`] = 10 - index;
    }
    const keys = operationsBreakdownKeys({
      history: {
        from: 1,
        points: [{ metrics, observedAt: 1000 }],
        stepMillis: 1000,
        to: 1000,
        totals: {},
      },
      otherKey: "otherCommandsPerSecond",
      prefix: "cmd.",
    });
    expect(keys).toHaveLength(8);
    expect(keys.at(-1)?.key).toBe("otherCommandsPerSecond");
    expect(keys.filter((item) => item.key.startsWith("cmd."))).toHaveLength(7);
  });
});

describe("foldUnselectedOperationsIntoOther", () => {
  test("moves unselected cmd rates into Other", () => {
    const folded = foldUnselectedOperationsIntoOther(
      {
        from: 1,
        points: [
          {
            metrics: {
              "cmd.del": 3,
              "cmd.get": 10,
              "cmd.set": 4,
              otherCommandsPerSecond: 1,
            },
            observedAt: 1000,
          },
        ],
        stepMillis: 1000,
        to: 1000,
        totals: {},
      },
      [{ key: "cmd.get" }, { key: "otherCommandsPerSecond" }],
      { otherKey: "otherCommandsPerSecond", prefix: "cmd." }
    );
    expect(folded?.points[0]?.metrics).toEqual({
      "cmd.get": 10,
      otherCommandsPerSecond: 8,
    });
  });
});
