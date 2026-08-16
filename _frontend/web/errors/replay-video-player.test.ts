import { describe, expect, test } from "bun:test";

import { replayVideoGaps, replayVideoSegmentAt } from "./replay-video-player";
import type { ReplayVideoSegment } from "./types";

const segments: ReplayVideoSegment[] = [
  { duration: 1000, id: 1, timestamp: 2000 },
  { duration: 1000, id: 2, timestamp: 5000 },
];

describe("mobile replay video timeline", () => {
  test("holds the previous segment while the replay clock crosses a gap", () => {
    expect(replayVideoSegmentAt(segments, 2500)).toBe(0);
    expect(replayVideoSegmentAt(segments, 4000)).toBe(0);
    expect(replayVideoSegmentAt(segments, 5500)).toBe(1);
  });

  test("preserves unavailable time before, between, and after segments", () => {
    expect(replayVideoGaps(segments, 0, 8000)).toEqual([
      { end: 2000, start: 0 },
      { end: 5000, start: 3000 },
      { end: 8000, start: 6000 },
    ]);
  });
});
