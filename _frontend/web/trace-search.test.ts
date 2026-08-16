import { describe, expect, test } from "bun:test";

import type { ServiceTraceSpan } from "@/api";

import { matchingTraceSpans } from "./trace-search";

const span = (overrides: Partial<ServiceTraceSpan> = {}): ServiceTraceSpan => ({
  aiAgent: "",
  aiCacheReadTokens: null,
  aiCacheWriteTokens: null,
  aiCostUsd: null,
  aiInputTokens: null,
  aiKind: "",
  aiModel: "",
  aiOperation: "",
  aiOutputTokens: null,
  aiProvider: "",
  aiReasoningTokens: null,
  aiTokensPerSecond: null,
  aiTtftSeconds: null,
  durationNano: "600000000",
  endTimeUnixNano: "1600000000",
  flags: 0,
  isSegment: true,
  kind: 2,
  name: "POST /checkout",
  parentSpanId: "",
  receivedAtUnixNano: "1600000000",
  resource: {
    attributes: [
      { key: "service.name", value: { stringValue: "checkout-api" } },
    ],
  },
  scope: {},
  segmentId: "a".repeat(16),
  serviceId: "service-test",
  source: "otlp",
  span: { attributes: [], op: "http.server" },
  spanId: "a".repeat(16),
  startTimeUnixNano: "1000000000",
  statusCode: 2,
  statusMessage: "",
  traceId: "b".repeat(32),
  traceState: "",
  ...overrides,
});

describe("trace search", () => {
  test("combines structured duration, status, operation, and service filters", () => {
    const spans = [span(), span({ name: "cache lookup", statusCode: 1 })];

    expect(
      matchingTraceSpans(
        spans,
        "duration:>500ms status:error op:http service:checkout"
      ).map((candidate) => candidate.name)
    ).toEqual(["POST /checkout"]);
  });

  test("finds Sentry issue markers without coupling normal OTEL spans to Sentry", () => {
    const marker = span({
      durationNano: "0",
      source: "sentry_error",
      span: { event_id: "event", issue_id: "issue" },
    });

    expect(matchingTraceSpans([span(), marker], "has:issue")).toEqual([marker]);
  });
});
