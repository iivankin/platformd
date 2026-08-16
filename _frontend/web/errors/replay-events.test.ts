import { describe, expect, test } from "bun:test";

import type { eventWithTime as RrwebEvent } from "@sentry/rrweb";

import {
  prepareReplayEvents,
  replayInteractionEvents,
  replayVideoSegments,
} from "./replay-events";

describe("prepare replay events", () => {
  test("separates Sentry auxiliary frames before constructing the rrweb stream", () => {
    const rrwebStart = 1_786_756_532_890;
    const events: RrwebEvent[] = [
      {
        data: { height: 720, href: "https://example.com", width: 1280 },
        timestamp: rrwebStart,
        type: 4 as const,
      },
      {
        data: {
          initialOffset: { left: 0, top: 0 },
          node: { childNodes: [], id: 1, type: 0 },
        },
        timestamp: rrwebStart + 10,
        type: 2 as const,
      },
      {
        data: {
          payload: { category: "navigation", timestamp: rrwebStart / 1000 - 1 },
          tag: "breadcrumb",
        },
        timestamp: rrwebStart / 1000,
        type: 5 as const,
      },
      {
        data: {
          payload: {
            endTimestamp: rrwebStart / 1000 + 5,
            op: "resource.fetch",
            startTimestamp: rrwebStart / 1000 - 0.5,
          },
          tag: "performanceSpan",
        },
        timestamp: rrwebStart / 1000,
        type: 5 as const,
      },
      {
        data: { payload: { sessionSampleRate: 1 }, tag: "options" },
        timestamp: rrwebStart / 1000,
        type: 5 as const,
      },
    ];

    const prepared = prepareReplayEvents(events);

    expect(prepared.startTime).toBe(rrwebStart - 1000);
    expect(prepared.endTime).toBe(rrwebStart + 5000);
    expect(prepared.duration).toBe(6000);
    expect(prepared.events.map((event) => event.type)).toEqual([4, 2, 4, 2, 5]);
    expect(prepared.events[0]?.timestamp).toBe(rrwebStart - 1000);
    expect(prepared.events[1]?.timestamp).toBe(rrwebStart - 1000);
    expect(prepared.events.at(-1)).toEqual({
      data: { payload: {}, tag: "replay.end" },
      timestamp: rrwebStart + 5000,
      type: 5,
    });
    expect(events).toHaveLength(5);
    expect(events[0]?.timestamp).toBe(rrwebStart);
  });

  test("does not let web-vital spans extend replay bounds", () => {
    const timestamp = 1_786_756_532_890;
    const prepared = prepareReplayEvents([
      {
        data: { height: 720, href: "https://example.com", width: 1280 },
        timestamp,
        type: 4 as const,
      },
      {
        data: {
          payload: {
            endTimestamp: timestamp / 1000 + 60,
            op: "web-vital",
            startTimestamp: timestamp / 1000,
          },
          tag: "performanceSpan",
        },
        timestamp: timestamp / 1000,
        type: 5 as const,
      },
    ]);

    expect(prepared.duration).toBe(0);
    expect(prepared.startTime).toBe(timestamp);
    expect(prepared.endTime).toBe(timestamp);
  });

  test("drops malformed frames instead of handing them to rrweb", () => {
    const timestamp = 1_786_756_532_890;
    const prepared = prepareReplayEvents([
      { data: {}, timestamp: Number.NaN, type: 4 },
      { data: {}, timestamp, type: 1.5 },
      { data: {}, timestamp, type: 4 },
    ] as RrwebEvent[]);

    expect(prepared.events).toHaveLength(2);
    expect(prepared.events[0]?.timestamp).toBe(timestamp);
    expect(prepared.events[1]?.data).toEqual({
      payload: {},
      tag: "replay.end",
    });
  });

  test("extracts sorted mobile replay video segments", () => {
    const segments = replayVideoSegments([
      {
        data: {
          payload: { duration: 2000, height: 720, segmentId: 2, width: 1280 },
          tag: "video",
        },
        timestamp: 3000,
        type: 5,
      },
      {
        data: {
          payload: { duration: 1000, segmentId: 1 },
          tag: "video",
        },
        timestamp: 1000,
        type: 5,
      },
      {
        data: {
          payload: { duration: 0, segmentId: 3 },
          tag: "video",
        },
        timestamp: 4000,
        type: 5,
      },
    ] as RrwebEvent[]);

    expect(segments).toEqual([
      {
        duration: 1000,
        height: undefined,
        id: 1,
        timestamp: 1000,
        width: undefined,
      },
      { duration: 2000, height: 720, id: 2, timestamp: 3000, width: 1280 },
    ]);
  });

  test("adds the snapshot rrweb needs for mobile gestures and makes taps visible", () => {
    const interactions = replayInteractionEvents([
      {
        data: { height: 800, width: 400 },
        timestamp: 1000,
        type: 4,
      },
      {
        data: { id: 2, source: 2, type: 7, x: 40, y: 80 },
        timestamp: 1100,
        type: 3,
      },
      {
        data: { id: 2, source: 2, type: 9, x: 40, y: 80 },
        timestamp: 1110,
        type: 3,
      },
    ] as RrwebEvent[]);

    expect(interactions.map((event) => event.type)).toEqual([4, 2, 3, 3]);
    expect(interactions[1]?.data).toMatchObject({ node: { id: 0, type: 0 } });
    expect(interactions[3]?.timestamp).toBe(1850);
  });
});
