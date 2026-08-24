import { describe, expect, test } from "bun:test";

import { formatTraceDuration } from "./trace-format";

describe("trace duration formatting", () => {
  test("uses stable units across sub-millisecond, millisecond, and second spans", () => {
    expect(formatTraceDuration("500000")).toBe("500 μs");
    expect(formatTraceDuration("5000000")).toBe("5.00 ms");
    expect(formatTraceDuration("1500000000")).toBe("1.50 s");
  });
});
