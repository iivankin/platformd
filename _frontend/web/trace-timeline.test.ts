import { describe, expect, test } from "bun:test";

import type { ServiceTraceSpan } from "@/api";

import { buildTraceTimeline, zoomTraceViewport } from "./trace-timeline";

const span = (start: bigint, end: bigint): ServiceTraceSpan => ({
  aiAgent: "",
  aiCacheReadTokens: null,
  aiCacheWriteTokens: null,
  aiCostUsd: null,
  aiEstimatedCostUsd: null,
  aiInputTokens: null,
  aiKind: "",
  aiModel: "",
  aiOperation: "",
  aiOutputTokens: null,
  aiProvider: "",
  aiReasoningTokens: null,
  aiSessionId: "",
  aiTokensPerSecond: null,
  aiTtftSeconds: null,
  aiUserId: "",
  durationNano: (end - start).toString(),
  endTimeUnixNano: end.toString(),
  flags: 0,
  kind: 1,
  name: "work",
  parentSpanId: "root",
  receivedAtUnixNano: end.toString(),
  replayId: "",
  resource: {},
  scope: {},
  serviceId: "service-test",
  source: "otlp",
  span: {},
  spanId: `${start}`.padEnd(16, "0").slice(0, 16),
  startTimeUnixNano: start.toString(),
  statusCode: 1,
  statusMessage: "",
  traceId: "0".repeat(32),
  traceState: "",
});

describe("trace timeline", () => {
  test("compresses long idle periods while preserving reversible positions", () => {
    const viewport = { end: 1000n, start: 0n };
    const timeline = buildTraceTimeline(
      [span(0n, 100n), span(900n, 1000n)],
      viewport,
      true
    );
    const secondStart = timeline.position(900n);

    expect(timeline.compressedGapCount).toBe(1);
    expect(secondStart).toBeLessThan(70);
    expect(timeline.timeAt(secondStart)).toBe(900n);
  });

  test("does not expand a sub-millisecond trace when zooming in", () => {
    const baseViewport = { end: 500_000n, start: 0n };

    expect(
      zoomTraceViewport(baseViewport, baseViewport, 250_000n, "in")
    ).toEqual(baseViewport);
  });
});
