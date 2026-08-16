import { EventType } from "@sentry/rrweb";
import type { eventWithTime as RrwebEvent } from "@sentry/rrweb";

import type { ReplayVideoSegment } from "./types";

const AUXILIARY_TAGS = new Set([
  "breadcrumb",
  "options",
  "performanceSpan",
  "video",
]);
const SPANS_EXCLUDED_FROM_BOUNDS = new Set([
  "largest-contentful-paint",
  "web-vital",
]);

export interface PreparedReplayEvents {
  duration: number;
  endTime: number;
  events: RrwebEvent[];
  startTime: number;
}

export interface ReplayTimeBounds {
  finishedAt?: number | null;
  startedAt?: number | null;
}

const record = (value: unknown): Record<string, unknown> | undefined =>
  typeof value === "object" && value !== null
    ? (value as Record<string, unknown>)
    : undefined;

const finiteNumber = (value: unknown): number | undefined =>
  typeof value === "number" && Number.isFinite(value) ? value : undefined;

const eventTag = (event: RrwebEvent) => {
  if (event.type !== EventType.Custom) {
    return;
  }
  const tag = record(event.data)?.tag;
  return typeof tag === "string" ? tag : undefined;
};

const isRecordingFrame = (value: unknown): value is RrwebEvent => {
  const frame = record(value);
  return (
    frame !== undefined &&
    Number.isInteger(frame.type) &&
    finiteNumber(frame.timestamp) !== undefined
  );
};

export const replayVideoSegments = (
  input: RrwebEvent[]
): ReplayVideoSegment[] =>
  input
    .flatMap((event) => {
      if (eventTag(event) !== "video") {
        return [];
      }
      const payload = record(record(event.data)?.payload);
      const id = finiteNumber(payload?.segmentId);
      const duration = finiteNumber(payload?.duration);
      if (id === undefined || duration === undefined || duration <= 0) {
        return [];
      }
      return [
        {
          duration,
          height: finiteNumber(payload?.height),
          id,
          timestamp: event.timestamp,
          width: finiteNumber(payload?.width),
        },
      ];
    })
    .sort(
      (left, right) => left.timestamp - right.timestamp || left.id - right.id
    );

const isTouchStart = (event: RrwebEvent) => {
  const data = record(event.data);
  return (
    event.type === EventType.IncrementalSnapshot &&
    data?.source === 2 &&
    data.type === 7
  );
};

const isTouchEnd = (event: RrwebEvent) => {
  const data = record(event.data);
  return (
    event.type === EventType.IncrementalSnapshot &&
    data?.source === 2 &&
    data.type === 9
  );
};

const isTouchMove = (event: RrwebEvent) => {
  const data = record(event.data);
  return event.type === EventType.IncrementalSnapshot && data?.source === 6;
};

const interactionSnapshot = (timestamp: number) =>
  ({
    data: {
      initialOffset: { left: 0, top: 0 },
      node: {
        childNodes: [
          {
            id: 1,
            name: "html",
            publicId: "",
            systemId: "",
            type: 1,
          },
          {
            attributes: { lang: "en" },
            childNodes: [],
            id: 2,
            tagName: "html",
            type: 2,
          },
        ],
        id: 0,
        type: 0,
      },
    },
    timestamp,
    type: EventType.FullSnapshot,
  }) as RrwebEvent;

// Mobile video replays contain rrweb pointer/touch frames but no DOM snapshot.
// rrweb needs a minimal document for gesture rendering, matching Sentry's
// VideoReplayerWithInteractions preparation. A visible tap is clamped to 750ms.
export const replayInteractionEvents = (input: RrwebEvent[]): RrwebEvent[] => {
  const frames = input
    .filter(isRecordingFrame)
    .filter((event) => !AUXILIARY_TAGS.has(eventTag(event) ?? ""))
    .filter((event) => eventTag(event) !== "replay.end")
    .map((event) => ({ ...event }))
    .toSorted((left, right) => left.timestamp - right.timestamp);

  for (let index = 0; index < frames.length - 1; index += 1) {
    const current = frames[index];
    const next = frames[index + 1];
    if (!(current && next && isTouchStart(current))) {
      continue;
    }
    if (isTouchEnd(next) || isTouchMove(next)) {
      next.timestamp = Math.max(next.timestamp, current.timestamp + 750);
    }
    const afterNext = frames[index + 2];
    if (afterNext && isTouchMove(next) && isTouchEnd(afterNext)) {
      afterNext.timestamp = Math.max(afterNext.timestamp, next.timestamp + 750);
    }
  }

  return frames.flatMap((event) =>
    event.type === EventType.Meta
      ? [event, interactionSnapshot(event.timestamp)]
      : [event]
  );
};

const updateBounds = (
  value: number | undefined,
  bounds: { end: number; start: number }
) => {
  if (value === undefined) {
    return;
  }
  bounds.start = Math.min(bounds.start, value);
  bounds.end = Math.max(bounds.end, value);
};

const consumeAuxiliaryFrame = (
  event: RrwebEvent,
  bounds: { end: number; start: number }
) => {
  const tag = eventTag(event);
  const payload = record(record(event.data)?.payload);

  if (tag === "breadcrumb") {
    const timestamp = finiteNumber(payload?.timestamp);
    updateBounds(
      timestamp === undefined ? undefined : timestamp * 1000,
      bounds
    );
    return true;
  }
  if (tag === "performanceSpan") {
    const op = typeof payload?.op === "string" ? payload.op : "";
    if (!SPANS_EXCLUDED_FROM_BOUNDS.has(op)) {
      const start = finiteNumber(payload?.startTimestamp);
      const end = finiteNumber(payload?.endTimestamp);
      updateBounds(start === undefined ? undefined : start * 1000, bounds);
      updateBounds(end === undefined ? undefined : end * 1000, bounds);
    }
    return true;
  }
  return tag !== undefined && AUXILIARY_TAGS.has(tag);
};

// Sentry stores replay breadcrumbs and performance spans beside rrweb frames,
// but hydrates them into separate streams before constructing the rrweb player.
// Their timestamps are seconds while rrweb uses milliseconds, so mixing the
// streams makes rrweb interpret the replay as lasting decades.
export const prepareReplayEvents = (
  input: RrwebEvent[],
  recordingBounds: ReplayTimeBounds = {}
): PreparedReplayEvents => {
  const events: RrwebEvent[] = [];
  const bounds = {
    end: Number.NEGATIVE_INFINITY,
    start: Number.POSITIVE_INFINITY,
  };
  updateBounds(finiteNumber(recordingBounds.startedAt), bounds);
  updateBounds(finiteNumber(recordingBounds.finishedAt), bounds);

  for (const candidate of input) {
    if (!isRecordingFrame(candidate)) {
      continue;
    }
    const event = candidate;
    const tag = eventTag(event);
    if (consumeAuxiliaryFrame(event, bounds)) {
      continue;
    }
    if (tag === "replay.end") {
      continue;
    }

    events.push(event);
    updateBounds(event.timestamp, bounds);
  }

  events.sort((left, right) => left.timestamp - right.timestamp);

  if (!Number.isFinite(bounds.start) || !Number.isFinite(bounds.end)) {
    return { duration: 0, endTime: 0, events, startTime: 0 };
  }
  if (events.length === 0) {
    return {
      duration: Math.max(0, bounds.end - bounds.start),
      endTime: bounds.end,
      events,
      startTime: bounds.start,
    };
  }

  const firstMeta = events.find((event) => event.type === EventType.Meta);
  const firstSnapshot = events.find(
    (event) => event.type === EventType.FullSnapshot
  );
  if (firstMeta && firstSnapshot && firstMeta.timestamp > bounds.start) {
    events.unshift(
      { ...firstMeta, timestamp: bounds.start },
      { ...firstSnapshot, timestamp: bounds.start }
    );
  }
  events.push({
    data: { payload: {}, tag: "replay.end" },
    timestamp: bounds.end,
    type: EventType.Custom,
  } as RrwebEvent);

  return {
    duration: Math.max(0, bounds.end - bounds.start),
    endTime: bounds.end,
    events,
    startTime: bounds.start,
  };
};
