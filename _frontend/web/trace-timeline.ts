import type { ServiceTraceSpan } from "@/api";

export interface TraceViewport {
  end: bigint;
  start: bigint;
}

interface TimelineSegment {
  displayEnd: number;
  displayStart: number;
  end: bigint;
  start: bigint;
}

export interface TraceTimeline {
  compressedGapCount: number;
  duration: bigint;
  position: (time: bigint) => number;
  timeAt: (percent: number) => bigint;
  viewport: TraceViewport;
}

const clippedIntervals = (
  spans: ServiceTraceSpan[],
  viewport: TraceViewport
) => {
  const intervals = spans
    .filter((span) => span.parentSpanId && BigInt(span.durationNano) > 0n)
    .map((span) => ({
      end:
        BigInt(span.endTimeUnixNano) > viewport.end
          ? viewport.end
          : BigInt(span.endTimeUnixNano),
      start:
        BigInt(span.startTimeUnixNano) < viewport.start
          ? viewport.start
          : BigInt(span.startTimeUnixNano),
    }))
    .filter((interval) => interval.end > interval.start)
    .toSorted((left, right) => (left.start < right.start ? -1 : 1));
  const merged: { end: bigint; start: bigint }[] = [];
  for (const interval of intervals) {
    const previous = merged.at(-1);
    if (previous && interval.start <= previous.end) {
      if (interval.end > previous.end) {
        previous.end = interval.end;
      }
    } else {
      merged.push({ ...interval });
    }
  }
  return merged;
};

export const traceViewport = (spans: ServiceTraceSpan[]): TraceViewport => {
  let start = BigInt(spans[0]?.startTimeUnixNano ?? "0");
  let end = start;
  for (const span of spans) {
    const spanStart = BigInt(span.startTimeUnixNano);
    const spanEnd = BigInt(span.endTimeUnixNano);
    if (spanStart < start) {
      start = spanStart;
    }
    if (spanEnd > end) {
      end = spanEnd;
    }
  }
  return { end: end > start ? end : start + 1n, start };
};

export const buildTraceTimeline = (
  spans: ServiceTraceSpan[],
  viewport: TraceViewport,
  compressGaps: boolean
): TraceTimeline => {
  const duration = viewport.end - viewport.start;
  const threshold = duration / 20n;
  const intervals = clippedIntervals(spans, viewport);
  const gaps: { end: bigint; start: bigint }[] = [];
  let cursor = viewport.start;
  for (const interval of intervals) {
    if (interval.start - cursor >= threshold) {
      gaps.push({ end: interval.start, start: cursor });
    }
    if (interval.end > cursor) {
      cursor = interval.end;
    }
  }
  if (viewport.end - cursor >= threshold) {
    gaps.push({ end: viewport.end, start: cursor });
  }

  const segments: TimelineSegment[] = [];
  let displayCursor = 0;
  cursor = viewport.start;
  for (const gap of compressGaps ? gaps : []) {
    if (gap.start > cursor) {
      const width = Number(gap.start - cursor);
      segments.push({
        displayEnd: displayCursor + width,
        displayStart: displayCursor,
        end: gap.start,
        start: cursor,
      });
      displayCursor += width;
    }
    const compressedWidth = Math.max(1, Number(duration) * 0.02);
    segments.push({
      displayEnd: displayCursor + compressedWidth,
      displayStart: displayCursor,
      end: gap.end,
      start: gap.start,
    });
    displayCursor += compressedWidth;
    cursor = gap.end;
  }
  if (cursor < viewport.end || segments.length === 0) {
    const width = Number(viewport.end - cursor);
    segments.push({
      displayEnd: displayCursor + width,
      displayStart: displayCursor,
      end: viewport.end,
      start: cursor,
    });
    displayCursor += width;
  }
  const displayDuration = Math.max(1, displayCursor);
  const segmentAtTime = (time: bigint) =>
    segments.find((segment) => time >= segment.start && time <= segment.end) ??
    (time < viewport.start ? segments[0] : segments.at(-1));
  const segmentAtDisplay = (position: number) =>
    segments.find(
      (segment) =>
        position >= segment.displayStart && position <= segment.displayEnd
    ) ?? segments.at(-1);

  return {
    compressedGapCount: compressGaps ? gaps.length : 0,
    duration,
    position: (time) => {
      let clamped = time;
      if (clamped < viewport.start) {
        clamped = viewport.start;
      } else if (clamped > viewport.end) {
        clamped = viewport.end;
      }
      const segment = segmentAtTime(clamped);
      if (!segment) {
        return 0;
      }
      const actualWidth = Number(segment.end - segment.start) || 1;
      const ratio = Number(clamped - segment.start) / actualWidth;
      const display =
        segment.displayStart +
        ratio * (segment.displayEnd - segment.displayStart);
      return (display / displayDuration) * 100;
    },
    timeAt: (percent) => {
      const display =
        (Math.min(100, Math.max(0, percent)) / 100) * displayDuration;
      const segment = segmentAtDisplay(display);
      if (!segment) {
        return viewport.start;
      }
      const displayWidth = segment.displayEnd - segment.displayStart || 1;
      const ratio = (display - segment.displayStart) / displayWidth;
      return (
        segment.start +
        BigInt(Math.round(Number(segment.end - segment.start) * ratio))
      );
    },
    viewport,
  };
};
