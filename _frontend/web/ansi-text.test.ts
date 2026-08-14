import { describe, expect, test } from "bun:test";

import { parseAnsi } from "@/ansi-text";

describe("ANSI log rendering", () => {
  test("preserves text while applying standard and extended SGR colors", () => {
    const segments = parseAnsi(
      "plain \u001B[2mdim\u001B[0m \u001B[36mcyan\u001B[38;2;12;34;56m rgb\u001B[0m"
    );

    expect(segments.map((segment) => segment.text).join("")).toBe(
      "plain dim cyan rgb"
    );
    expect(
      segments.find((segment) => segment.text === "dim")?.style.opacity
    ).toBe(0.65);
    expect(
      segments.find((segment) => segment.text === "cyan")?.style.color
    ).toBe("#06b6d4");
    expect(
      segments.find((segment) => segment.text === " rgb")?.style.color
    ).toBe("rgb(12 34 56)");
  });

  test("accepts replacement characters left by container log decoding", () => {
    const segments = parseAnsi("\uFFFD[31mfailed\uFFFD[0m");

    expect(segments).toHaveLength(1);
    expect(segments[0]?.text).toBe("failed");
    expect(segments[0]?.style.color).toBe("#ef4444");
  });
});
