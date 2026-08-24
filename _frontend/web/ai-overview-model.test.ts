import { describe, expect, test } from "bun:test";

import type { AiOverview } from "@/api";

import {
  emptyPricedUsage,
  formatPricedUsageCost,
  formatUserCost,
  overviewAnalysis,
  userRows,
} from "./ai-overview-model";

const unixNano = (timestamp: string) =>
  (BigInt(Date.parse(timestamp)) * 1_000_000n).toString();

const overview = (): AiOverview => ({
  activity: [],
  agents: [],
  latency: [],
  modelUsage: [],
  models: [],
  summary: {
    agentCount: 0,
    agentRunCount: 0,
    errorCount: 0,
    generationCount: 0,
    identifiedAgentRunCount: 0,
    modelCount: 0,
    sessionCount: 0,
    toolCallCount: 0,
    userCount: 0,
  },
  usage: [],
  users: [],
});

describe("AI overview aggregation", () => {
  test("formats unknown, reported, estimated, and partial costs distinctly", () => {
    expect(formatPricedUsageCost(emptyPricedUsage(), false)).toBe("$0");
    expect(formatPricedUsageCost(emptyPricedUsage())).toBe("—");
    expect(
      formatPricedUsageCost({
        cost: 0.25,
        estimatedCost: false,
        hasPrice: true,
        partialCost: false,
      })
    ).toBe("$0.250");
    expect(
      formatPricedUsageCost({
        cost: 0.25,
        estimatedCost: true,
        hasPrice: true,
        partialCost: true,
      })
    ).toBe("~$0.250+");
  });

  test("combines stored estimates without recalculating model prices", () => {
    const input = overview();
    input.usage = [
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 120,
        estimatedCostUsd: 0.8,
        inputTokens: 1_000_000,
        outputTokens: 0,
        reasoningTokens: 0,
        reportedCostUsd: null,
        timeUnixNano: unixNano("2026-07-29T00:00:00Z"),
      },
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 80,
        estimatedCostUsd: 1.6,
        inputTokens: 1_000_000,
        outputTokens: 0,
        reasoningTokens: 0,
        reportedCostUsd: null,
        timeUnixNano: unixNano("2026-07-31T00:00:00Z"),
      },
    ];
    input.modelUsage = input.usage.map(({ timeUnixNano: _, ...usage }) => ({
      ...usage,
      model: "gpt-5.6-luna",
      provider: "openai",
    }));

    const result = overviewAnalysis(input);
    expect(result.total).toMatchObject({
      cacheReadTokens: 0,
      cacheWriteTokens: 200,
      estimatedCost: true,
      inputTokens: 2_000_000,
      outputTokens: 0,
      reasoningTokens: 0,
    });
    expect(result.total.cost).toBeCloseTo(2.4);
    expect(result.byModel.get("openai\u0000gpt-5.6-luna")?.cost).toBeCloseTo(
      2.4
    );
  });

  test("does not multiply repeated user statistics across cost provenance", () => {
    const input = overview();
    input.users = [
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCostUsd: null,
        generationCount: 1,
        inputTokens: 10,
        outputTokens: 4,
        reportedCostUsd: 0.1,
        runCount: 3,
        sessionCount: 2,
        userId: "user-42",
      },
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCostUsd: null,
        generationCount: 1,
        inputTokens: 20,
        outputTokens: 6,
        reportedCostUsd: 0.2,
        runCount: 3,
        sessionCount: 2,
        userId: "user-42",
      },
    ];

    const [user] = userRows(input);
    expect(user).toMatchObject({
      estimatedCost: false,
      generations: 2,
      hasPrice: true,
      partialCost: false,
      runs: 3,
      sessions: 2,
      tokens: 40,
      userId: "user-42",
    });
    expect(user?.cost).toBeCloseTo(0.3);
  });

  test("keeps unknown user cost distinct from a zero cost", () => {
    const input = overview();
    input.users = [
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCostUsd: null,
        generationCount: 1,
        inputTokens: 10,
        outputTokens: 4,
        reportedCostUsd: null,
        runCount: 1,
        sessionCount: 1,
        userId: "user-unknown",
      },
    ];

    expect(userRows(input)[0]).toMatchObject({
      cost: 0,
      estimatedCost: false,
      hasPrice: false,
      partialCost: true,
    });
  });

  test("formats a user without generations as an exact zero cost", () => {
    const input = overview();
    input.users = [
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCostUsd: null,
        generationCount: 0,
        inputTokens: 0,
        outputTokens: 0,
        reportedCostUsd: null,
        runCount: 1,
        sessionCount: 1,
        userId: "agent-only-user",
      },
    ];
    const [user] = userRows(input);

    expect(user && formatUserCost(user)).toBe("$0");
  });

  test("preserves cost provenance for every timeline bucket", () => {
    const input = overview();
    const reportedAt = unixNano("2026-08-01T00:00:00Z");
    const estimatedAt = unixNano("2026-08-01T01:00:00Z");
    const unknownAt = unixNano("2026-08-01T02:00:00Z");
    input.usage = [
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCostUsd: null,
        inputTokens: 10,
        outputTokens: 5,
        reasoningTokens: 0,
        reportedCostUsd: 0.25,
        timeUnixNano: reportedAt,
      },
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCostUsd: null,
        inputTokens: 10,
        outputTokens: 5,
        reasoningTokens: 0,
        reportedCostUsd: null,
        timeUnixNano: reportedAt,
      },
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCostUsd: 0.001,
        inputTokens: 1000,
        outputTokens: 100,
        reasoningTokens: 0,
        reportedCostUsd: null,
        timeUnixNano: estimatedAt,
      },
      {
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCostUsd: null,
        inputTokens: 10,
        outputTokens: 5,
        reasoningTokens: 0,
        reportedCostUsd: null,
        timeUnixNano: unknownAt,
      },
    ];

    const { points } = overviewAnalysis(input);
    expect(points[0]).toMatchObject({
      cost: 25,
      estimatedCost: false,
      hasPrice: true,
      partialCost: true,
    });
    expect(points[1]).toMatchObject({
      estimatedCost: true,
      hasPrice: true,
      partialCost: false,
    });
    expect(points[2]).toMatchObject({
      cost: 0,
      estimatedCost: false,
      hasPrice: false,
      partialCost: true,
    });
  });

  test("represents an empty server bucket as zero activity and zero cost", () => {
    const input = overview();
    input.activity = [
      {
        agentRunCount: 0,
        errorCount: 0,
        generationCount: 0,
        timeUnixNano: unixNano("2026-08-01T03:00:00Z"),
        toolCallCount: 0,
      },
    ];

    expect(overviewAnalysis(input).points).toEqual([
      expect.objectContaining({
        cost: 0,
        generations: 0,
        hasPrice: true,
        partialCost: false,
      }),
    ]);
  });
});
