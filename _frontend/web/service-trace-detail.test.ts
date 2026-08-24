import { describe, expect, test } from "bun:test";

import type { ServiceTraceSpan } from "@/api";

import { traceServiceName } from "./trace-service-name";

const span = (resourceName: string): ServiceTraceSpan => ({
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
  durationNano: "1",
  endTimeUnixNano: "2",
  flags: 0,
  kind: 1,
  name: "span",
  parentSpanId: "",
  receivedAtUnixNano: "2",
  replayId: "",
  resource: {
    attributes: [{ key: "service.name", value: { stringValue: resourceName } }],
  },
  scope: {},
  serviceId: "service-1",
  source: "otlp",
  span: {},
  spanId: "span",
  startTimeUnixNano: "1",
  statusCode: 1,
  statusMessage: "",
  traceId: "trace",
  traceState: "",
});

const resolveServiceName = (serviceID: string) =>
  serviceID === "service-1" ? "dashboard" : undefined;

describe("trace service name", () => {
  test("replaces OpenTelemetry unknown_service with the registered name", () => {
    expect(
      traceServiceName(
        span("unknown_service:/app/dashboard"),
        resolveServiceName
      )
    ).toBe("dashboard");
    expect(traceServiceName(span("checkout-api"), resolveServiceName)).toBe(
      "checkout-api"
    );
    expect(
      traceServiceName(span("unknown_service_worker"), resolveServiceName)
    ).toBe("unknown_service_worker");
  });
});
