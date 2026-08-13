import { describe, expect, test } from "bun:test";

import { framesFromStacktrace, sourceContextLines } from "./event-detail";

describe("event stack source context", () => {
  test("keeps Sentry context and derives source line numbers", () => {
    const [frame] = framesFromStacktrace({
      frames: [
        {
          colno: 12,
          context_line: "throw new Error(message);",
          filename: "src/checkout.ts",
          function: "submitOrder",
          lineno: 42,
          post_context: ["}", ""],
          pre_context: ["if (!order) {", "  const message = 'missing';"],
        },
      ],
    });

    expect(frame).toBeDefined();
    if (!frame) {
      throw new Error("stack frame was not decoded");
    }
    expect(sourceContextLines(frame)).toEqual([
      { active: false, number: 40, text: "if (!order) {" },
      { active: false, number: 41, text: "  const message = 'missing';" },
      { active: true, number: 42, text: "throw new Error(message);" },
      { active: false, number: 43, text: "}" },
      { active: false, number: 44, text: "" },
    ]);
  });
});
