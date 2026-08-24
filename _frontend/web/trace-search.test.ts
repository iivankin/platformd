import { describe, expect, test } from "bun:test";

import type { ServiceTraceSpan } from "@/api";

import { matchingTraceSpans } from "./trace-search";

const span = (overrides: Partial<ServiceTraceSpan> = {}): ServiceTraceSpan => ({
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
  durationNano: "600000000",
  endTimeUnixNano: "1600000000",
  flags: 0,
  kind: 2,
  name: "POST /checkout",
  parentSpanId: "",
  receivedAtUnixNano: "1600000000",
  replayId: "",
  resource: {
    attributes: [
      { key: "service.name", value: { stringValue: "checkout-api" } },
    ],
  },
  scope: {},
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

  test("does not serialize raw payloads for structured-only filters", () => {
    const candidate = span({
      resource: {
        toJSON: () => {
          throw new Error("resource should not be serialized");
        },
      },
      span: {
        toJSON: () => {
          throw new Error("span should not be serialized");
        },
      },
    });

    expect(
      matchingTraceSpans([candidate], "status:error duration:>500ms")
    ).toEqual([candidate]);
  });

  test("finds Sentry issue markers without coupling normal OTEL spans to Sentry", () => {
    const marker = span({
      durationNano: "0",
      source: "sentry_error",
      span: { event_id: "event", issue_id: "issue" },
    });

    expect(matchingTraceSpans([span(), marker], "has:issue")).toEqual([marker]);
  });

  test("searches the registered name used for synthetic OTEL services", () => {
    const synthetic = span({
      resource: {
        attributes: [
          {
            key: "service.name",
            value: { stringValue: "unknown_service:/app/dashboard" },
          },
        ],
      },
    });

    expect(
      matchingTraceSpans([synthetic], "service:dashboard", (serviceID) =>
        serviceID === "service-test" ? "dashboard" : undefined
      )
    ).toEqual([synthetic]);
  });

  test("filters by self time with overlapping child spans", () => {
    const root = span({
      durationNano: "1000000000",
      endTimeUnixNano: "2000000000",
    });
    const children = [
      span({
        endTimeUnixNano: "1600000000",
        name: "first child",
        parentSpanId: root.spanId,
        spanId: "c".repeat(16),
        startTimeUnixNano: "1200000000",
      }),
      span({
        endTimeUnixNano: "1900000000",
        name: "second child",
        parentSpanId: root.spanId,
        spanId: "d".repeat(16),
        startTimeUnixNano: "1500000000",
      }),
    ];

    expect(
      matchingTraceSpans([root, ...children], "self:300ms").map(
        (candidate) => candidate.name
      )
    ).toEqual(["POST /checkout"]);
  });
});
