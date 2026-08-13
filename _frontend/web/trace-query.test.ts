import { describe, expect, test } from "bun:test";

import { matchesTraceQuery, traceSearchText } from "@/trace-query";

const okTrace = { durationNano: "750000000", errorSpanCount: 0 };
const errorTrace = { durationNano: "1500000000", errorSpanCount: 1 };

describe("trace query directives", () => {
  test("removes directives from server search regardless of case", () => {
    expect(
      traceSearchText("checkout Status:error DURATION:>500ms payment")
    ).toBe("checkout payment");
  });

  test("matches only supported statuses", () => {
    expect(matchesTraceQuery(errorTrace, "Status:error")).toBe(true);
    expect(matchesTraceQuery(okTrace, "status:ok")).toBe(true);
    expect(matchesTraceQuery(okTrace, "status:unknown")).toBe(false);
  });

  test("rejects malformed durations", () => {
    expect(matchesTraceQuery(errorTrace, "DURATION:>1s")).toBe(true);
    expect(matchesTraceQuery(okTrace, "duration:fast")).toBe(false);
  });
});
