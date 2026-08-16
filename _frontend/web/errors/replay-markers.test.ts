import { describe, expect, test } from "bun:test";

import { replayMarkers } from "./replay-markers";
import { groupReplayMarkers } from "./replay-timeline";
import type { ReplayRecording } from "./types";

const start = Date.parse("2026-08-09T10:44:30Z");

const recording = (events: ReplayRecording["events"]): ReplayRecording => ({
  errorEvents: [],
  events,
  replayId: "82818281828142818281828182818281",
  segmentCount: 1,
});

describe("replay activity markers", () => {
  test("labels mobile lifecycle and device breadcrumbs", () => {
    const timestamp = 1_786_756_532_890;
    const events = [
      {
        data: {
          payload: {
            category: "app.background",
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
            category: "device.battery",
            data: { charging: false, level: 47 },
            timestamp: timestamp / 1000 + 1,
          },
          tag: "breadcrumb",
        },
        timestamp: timestamp + 1000,
        type: 5,
      },
    ] satisfies ReplayRecording["events"];

    expect(replayMarkers(recording(events))).toMatchObject([
      { label: "App in background" },
      {
        detail: "Device was at 47% battery and not charging",
        label: "Device battery",
      },
    ]);
  });

  test("deduplicates CLS bursts and omits deprecated FID", () => {
    const timestamp = 1_786_756_532_890;
    const span = (description: string, offset = 0) => ({
      data: {
        payload: {
          description,
          op: "web-vital",
          startTimestamp: timestamp / 1000 + offset,
        },
        tag: "performanceSpan",
      },
      timestamp: timestamp + offset * 1000,
      type: 5 as const,
    });
    const markers = replayMarkers(
      recording([
        span("cumulative-layout-shift"),
        span("cumulative-layout-shift"),
        span("first-input-delay", 1),
      ])
    );

    expect(markers).toHaveLength(1);
    expect(markers[0]?.label).toBe("Web vital · Cumulative Layout Shift");
  });
});

describe("replay markers", () => {
  test("extracts Sentry frames, rrweb interactions, and linked errors", () => {
    const value = {
      errorEvents: [
        {
          doc_kind: "event",
          event_id: "event",
          level: "error",
          received_at: "2026-08-09T10:44:50Z",
          service_id: "service",
          timestamp: "2026-08-09T10:44:50Z",
          title: "Checkout failed",
        },
      ],
      events: [
        {
          data: { height: 720, href: "https://example.com/cart", width: 1280 },
          timestamp: start,
          type: 4,
        },
        {
          data: {
            payload: {
              category: "navigation",
              data: { from: "/cart", to: "/checkout" },
              timestamp: (start + 3000) / 1000,
            },
            tag: "breadcrumb",
          },
          timestamp: start + 3000,
          type: 5,
        },
        {
          data: {
            payload: {
              description: "POST /api/checkout",
              op: "resource.fetch",
              startTimestamp: (start + 10_000) / 1000,
            },
            tag: "performanceSpan",
          },
          timestamp: start + 10_000,
          type: 5,
        },
        {
          data: {
            id: 4,
            isChecked: false,
            source: 5,
            text: "masked",
            userTriggered: true,
          },
          timestamp: start + 15_000,
          type: 3,
        },
        {
          data: {
            payload: {
              category: "ui.click",
              message: "button[type=submit]",
              timestamp: (start + 18_000) / 1000,
            },
            tag: "breadcrumb",
          },
          timestamp: start + 18_000,
          type: 5,
        },
        {
          data: { id: 5, source: 2, type: 2, x: 10, y: 10 },
          timestamp: start + 18_000,
          type: 3,
        },
      ],
      replayId: "replay",
      segmentCount: 1,
    } satisfies ReplayRecording;

    const markers = replayMarkers(value);
    expect(markers.map((marker) => marker.kind)).toEqual([
      "navigation",
      "navigation",
      "network",
      "interaction",
      "interaction",
      "error",
    ]);
    expect(
      markers.filter(
        (marker) =>
          marker.kind === "interaction" && marker.timestamp === start + 18_000
      )
    ).toEqual([
      expect.objectContaining({
        detail: "button[type=submit]",
        label: "User click",
      }),
    ]);
  });

  test("groups nearby events and clamps out-of-range timestamps", () => {
    const groups = groupReplayMarkers(
      [
        { kind: "network", label: "Request", timestamp: start - 1000 },
        { kind: "warning", label: "Slow click", timestamp: start + 5000 },
        { kind: "error", label: "Error", timestamp: start + 5100 },
        { kind: "interaction", label: "Click", timestamp: start + 20_000 },
      ],
      start,
      10_000
    );

    expect(groups).toHaveLength(3);
    expect(groups[0]?.position).toBe(0);
    expect(groups[1]?.markers).toHaveLength(2);
    expect(groups[2]?.position).toBe(100);
  });
});
