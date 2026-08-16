import { describe, expect, test } from "bun:test";

import { telemetryHistogramBins } from "@/telemetry-histogram";
import { formatTelemetryRange } from "@/telemetry-time-range";

describe("telemetry histogram", () => {
  test("uses stable selected-range buckets and includes the end boundary", () => {
    const bins = telemetryHistogramBins(
      [
        { timestamp: 1000 },
        { error: true, timestamp: 1500 },
        { timestamp: 2000 },
      ],
      { from: 1000, to: 2000 }
    );

    expect(bins).toHaveLength(28);
    expect(bins[0]?.total).toBe(1);
    expect(bins.reduce((total, bin) => total + bin.total, 0)).toBe(3);
    expect(bins.reduce((total, bin) => total + bin.errors, 0)).toBe(1);
    expect(bins.at(-1)?.total).toBe(1);
  });

  test("keeps second precision for a clicked sub-minute bucket", () => {
    const from = Date.UTC(2026, 6, 14, 13, 58, 5);
    const label = formatTelemetryRange(from, from + 32_000, "en-US");

    expect(label).toContain("1:58:05");
    expect(label).toContain("1:58:37");
  });

  test("keeps millisecond precision when trace buckets are sub-second", () => {
    const from = Date.UTC(2026, 6, 14, 13, 58, 5, 125);
    const label = formatTelemetryRange(from, from + 450, "en-US");

    expect(label).toContain("1:58:05.125");
    expect(label).toContain("1:58:05.575");
  });
});
