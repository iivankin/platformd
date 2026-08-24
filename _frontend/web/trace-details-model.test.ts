import { describe, expect, test } from "bun:test";

import type { ServiceTraceSpan, ServiceTraceWebVital } from "@/api";

import {
  collapsibleTraceSpanIDs,
  formatWebVital,
  traceRoot,
  traceRows,
  traceWebVitals,
  webVitalTimelineMarkers,
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
  durationNano: "1000000",
  endTimeUnixNano: String(BigInt(start) + 1_000_000n),
  flags: 0,
  kind: 1,
  name: id,
  parentSpanId,
  receivedAtUnixNano: start,
  replayId: "",
  resource: {},
  scope: {},
  serviceId: "service-test",
  source: "otlp",
  span: payload,
  spanId: id.padEnd(16, "0").slice(0, 16),
  startTimeUnixNano: start,
  statusCode: 1,
  statusMessage: "",
  traceId: "0".repeat(32),
  traceState: "",
});

const webVital = (
  name: string,
  value: number,
  rating: ServiceTraceWebVital["rating"]
): ServiceTraceWebVital => ({
  delta: value,
  id: `vital-${name}`,
  name,
  navigationType: "navigate",
  rating,
  spanId: "1".repeat(16),
  timeUnixNano: "1000000000",
  value,
});

describe("trace details model", () => {
  test("formats correlated OpenTelemetry Web Vital logs", () => {
    const vitals = traceWebVitals([
      webVital("lcp", 1842, "good"),
      webVital("cls", 0.14, "needs-improvement"),
      webVital("ttfb", 120, "good"),
    ]);

    expect(vitals.map((entry) => entry.key)).toEqual(["lcp", "cls", "ttfb"]);
    const [lcp, cls, ttfb] = vitals;
    if (!(lcp && cls && ttfb)) {
      throw new Error("expected all fixture web vitals");
    }
    expect(formatWebVital(lcp)).toBe("1.84 s");
    expect(lcp.spanId).toBe("1".repeat(16));
    expect(cls.status).toBe("needs-improvement");
    expect(formatWebVital(ttfb)).toBe("120 ms");
  });

  test("anchors paint milestones to their correlated span", () => {
    const anchor = span("anchor", "", "5000000000");
    anchor.spanId = "1".repeat(16);
    const vitals = traceWebVitals([webVital("lcp", 1200, "good")]);
    const [vital] = vitals;
    if (!vital) {
      throw new Error("expected an LCP measurement");
    }

    expect(webVitalTimelineMarkers(vitals, [anchor], 1n)).toEqual([
      {
        timestampUnixNano: 6_200_000_000n,
        vital,
      },
    ]);
  });

  test("does not create a timeline marker for INP", () => {
    const vitals = traceWebVitals([webVital("inp", 4000, "poor")]);
    expect(webVitalTimelineMarkers(vitals, [], 1_000_000_000n)).toEqual([]);
  });

  test("uses standard Web Vital thresholds when a rating is missing", () => {
    const good = traceWebVitals([
      webVital("fcp", 1800, ""),
      webVital("lcp", 2500, ""),
      webVital("ttfb", 800, ""),
    ]);
    const needsImprovement = traceWebVitals([
      webVital("fcp", 3000, ""),
      webVital("lcp", 4000, ""),
      webVital("ttfb", 1800, ""),
    ]);

    expect(good.map((vital) => vital.status)).toEqual(["good", "good", "good"]);
    expect(needsImprovement.map((vital) => vital.status)).toEqual([
      "needs-improvement",
      "needs-improvement",
      "needs-improvement",
    ]);
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

  test("searches a deeply nested trace without recursive traversal", () => {
    let parentSpanId = "";
    const spans = Array.from({ length: 5000 }, (_, index) => {
      const current = span(
        index === 4999 ? "needle" : `node-${index}`,
        parentSpanId,
        String(1_000_000_000 + index)
      );
      current.spanId = index.toString(16).padStart(16, "0");
      parentSpanId = current.spanId;
      return current;
    });

    expect(traceRows(spans, new Set(), "needle")).toHaveLength(5000);
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

  test("finds collapsible spans without building display rows", () => {
    const root = span("root");
    const child = span("child", root.spanId);
    const grandchild = span("grandchild", child.spanId);
    const orphan = span("orphan", "missing-parent");

    expect(
      [...collapsibleTraceSpanIDs([root, child, grandchild, orphan])].toSorted()
    ).toEqual([child.spanId, root.spanId].toSorted());
  });

  test("selects a remote service entry when the trace root is outside scope", () => {
    const first = span("child", "outside000000000", "1000000000");
    const entry = span("entry", "remote0000000000", "1001000000");
    entry.flags = 512;

    expect(traceRoot([first, entry])?.name).toBe("entry");
  });

  test("prefers the parentless root over an earlier remote service entry", () => {
    const entry = span("entry", "outside000000000", "1000000000");
    entry.flags = 512;
    const root = span("root", "", "1001000000");

    expect(traceRoot([entry, root])?.name).toBe("root");
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

  test("does not group semantically different AI spans", () => {
    const root = span("root");
    const toolChildren = Array.from({ length: 5 }, (_, index) => {
      const child = span(
        "execute_tool",
        root.spanId,
        String(1_000_000_000 + index)
      );
      child.aiKind = "tool";
      child.spanId = `tool${index}`.padEnd(16, "0");
      child.span = {
        attributes: [
          {
            key: "gen_ai.tool.name",
            value: { stringValue: `tool-${index}` },
          },
        ],
      };
      return child;
    });
    const modelChildren = Array.from({ length: 5 }, (_, index) => {
      const child = span(
        "generate_text",
        root.spanId,
        String(1_100_000_000 + index)
      );
      child.aiKind = "model";
      child.aiModel = `model-${index}`;
      child.aiProvider = "openai";
      child.spanId = `model${index}`.padEnd(16, "0");
      return child;
    });

    const rows = traceRows(
      [root, ...toolChildren, ...modelChildren],
      new Set(),
      "",
      { autoGroup: true }
    );

    expect(rows.filter((row) => row.kind === "group")).toHaveLength(0);
    expect(rows.filter((row) => row.kind === "span")).toHaveLength(11);
  });
});
