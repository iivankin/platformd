import { describe, expect, test } from "bun:test";

import { replayInspectorData } from "./replay-inspector-data";
import type { ReplayRecording } from "./types";

describe("replay inspector data", () => {
  test("derives console, network, memory, and trace views from Sentry replay events", () => {
    const timestamp = 1_786_756_532_890;
    const recording = {
      errorEvents: [],
      events: [
        {
          data: {
            payload: {
              category: "console",
              data: { arguments: ["checkout", { failed: true }] },
              level: "error",
              timestamp: timestamp / 1000,
            },
            tag: "breadcrumb",
          },
          timestamp,
          type: 5,
        },
        {
          data: {
            payload: {
              data: { method: "POST", statusCode: 500, url: "/checkout" },
              endTimestamp: timestamp / 1000 + 0.15,
              op: "resource.fetch",
              startTimestamp: timestamp / 1000,
              trace_id: "4b25bc58f14243d8b208d1e22a054164",
            },
            tag: "performanceSpan",
          },
          timestamp,
          type: 5,
        },
        {
          data: {
            payload: {
              data: {
                memory: {
                  jsHeapSizeLimit: 1000,
                  totalJSHeapSize: 600,
                  usedJSHeapSize: 420,
                },
              },
              op: "memory",
              startTimestamp: timestamp / 1000 + 1,
            },
            tag: "performanceSpan",
          },
          timestamp: timestamp + 1000,
          type: 5,
        },
      ],
      replayId: "82818281828142818281828182818281",
      segmentCount: 1,
      traceIds: [],
    } satisfies ReplayRecording;

    const data = replayInspectorData(recording);

    expect(data.console[0]?.title).toBe('checkout {"failed":true}');
    expect(data.console[0]?.level).toBe("error");
    expect(data.network[0]).toMatchObject({
      detail: "/checkout",
      meta: "500 · 150 ms",
      title: "POST",
    });
    expect(data.memory[0]).toMatchObject({
      heapLimit: 1000,
      totalHeap: 600,
      usedHeap: 420,
    });
    expect(data.traceIDs).toEqual(["4b25bc58f14243d8b208d1e22a054164"]);
  });

  test("treats mobile Logcat and Timber breadcrumbs as console output", () => {
    const timestamp = 1_786_756_532_890;
    const recording = {
      errorEvents: [],
      events: ["Logcat", "Timber"].map((category, index) => ({
        data: {
          payload: {
            category,
            level: index === 0 ? "error" : "info",
            message: `${category} message`,
            timestamp: timestamp / 1000 + index,
          },
          tag: "breadcrumb",
        },
        timestamp: timestamp + index * 1000,
        type: 5 as const,
      })),
      replayId: "82818281828142818281828182818281",
      segmentCount: 1,
      traceIds: [],
    } satisfies ReplayRecording;

    expect(replayInspectorData(recording).console).toMatchObject([
      { level: "error", title: "Logcat message" },
      { level: "info", title: "Timber message" },
    ]);
  });
});
