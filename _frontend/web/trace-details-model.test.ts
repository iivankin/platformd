import { describe, expect, test } from "bun:test";

import type { ServiceTraceSpan } from "@/api";

import {
  formatWebVital,
  traceRows,
  traceWebVitals,
} from "./trace-details-model";

const span = (
  id: string,
  parentSpanId = "",
  start = "1000000000",
  payload: unknown = {}
): ServiceTraceSpan => ({
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
  durationNano: "1000000",
  endTimeUnixNano: String(BigInt(start) + 1_000_000n),
  flags: 0,
  isSegment: parentSpanId === "",
  kind: 1,
  name: id,
  parentSpanId,
  receivedAtUnixNano: start,
  resource: {},
  scope: {},
  segmentId: "root000000000000",
  serviceId: "service-test",
  source: "sentry",
  span: payload,
  spanId: id.padEnd(16, "0").slice(0, 16),
  startTimeUnixNano: start,
  statusCode: 1,
  statusMessage: "",
  traceId: "0".repeat(32),
  traceState: "",
});

describe("trace details model", () => {
  test("extracts and formats standard Sentry web vitals", () => {
    const vitals = traceWebVitals([
      span("root", "", "1000000000", {
        measurements: {
          cls: { unit: "none", value: 0.14 },
          lcp: { unit: "millisecond", value: 1842 },
          ttfb: { unit: "second", value: 0.12 },
        },
      }),
    ]);

    expect(vitals.map((vital) => vital.key)).toEqual(["lcp", "cls", "ttfb"]);
    const [lcp, cls, ttfb] = vitals;
    if (!(lcp && cls && ttfb)) {
      throw new Error("expected all fixture web vitals");
    }
    expect(formatWebVital(lcp)).toBe("1.84 s");
    expect(cls.status).toBe("needs-improvement");
    expect(formatWebVital(ttfb)).toBe("120 ms");
  });

  test("keeps matching descendants and their ancestors while searching", () => {
    const spans = [
      span("root"),
      span("child", "root000000000000", "1001000000"),
      span("query-hit", "child00000000000", "1002000000"),
    ];
    expect(
      traceRows(spans, new Set(), "query-hit").map((row) => row.span.name)
    ).toEqual(["root", "child", "query-hit"]);
  });

  test("collapses descendants without losing sibling order", () => {
    const spans = [
      span("root"),
      span("second", "root000000000000", "1002000000"),
      span("first", "root000000000000", "1001000000"),
    ];
    expect(traceRows(spans, new Set(), "").map((row) => row.span.name)).toEqual(
      ["root", "first", "second"]
    );
    expect(
      traceRows(spans, new Set(["root000000000000"]), "").map(
        (row) => row.span.name
      )
    ).toEqual(["root"]);
  });

  test("groups repeated siblings and exposes long uninstrumented gaps", () => {
    const root = span("root");
    root.durationNano = "1000000000";
    root.endTimeUnixNano = "2000000000";
    const children = Array.from({ length: 5 }, (_, index) => {
      const child = span(
        "repeat",
        "root000000000000",
        String(1_100_000_000 + index * 10_000_000)
      );
      child.spanId = `child${index}`.padEnd(16, "0");
      return child;
    });
    const rows = traceRows([root, ...children], new Set(), "", {
      autoGroup: true,
      showGaps: true,
    });

    expect(
      rows.some((row) => row.kind === "group" && row.childCount === 5)
    ).toBe(true);
    expect(rows.some((row) => row.kind === "gap")).toBe(true);
  });
});
