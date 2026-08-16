import { describe, expect, test } from "bun:test";

import { framesFromStacktrace, sourceContextLines } from "./event-detail";
import { eventStackGroups, relevantStackFrames } from "./event-stack";
import type { EventDetail } from "./types";

const detail = (payload: unknown): EventDetail => ({
  event: {
    doc_kind: "event",
    payload,
    received_at: "2026-08-16T00:00:00Z",
    service_id: "service-test",
    timestamp: "2026-08-16T00:00:00Z",
  },
});

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

  test("keeps chained exceptions visible in Relevant mode", () => {
    const groups = eventStackGroups(
      detail({
        exception: {
          values: [
            {
              mechanism: { exception_id: 1, handled: false, type: "generic" },
              stacktrace: {
                frames: [
                  { filename: "vendor.ts", in_app: false },
                  { filename: "app.ts", in_app: true },
                ],
              },
              type: "ApplicationError",
            },
            {
              mechanism: { exception_id: 2, parent_id: 1 },
              stacktrace: {
                frames: [
                  { filename: "runtime-a.js", in_app: false },
                  { filename: "runtime-b.js", in_app: false },
                ],
              },
              type: "RuntimeCause",
            },
          ],
        },
      })
    );

    expect(groups[1]?.relationship).toBe("Child of ApplicationError");
    expect(relevantStackFrames(groups[0]?.frames ?? [])).toHaveLength(2);
    expect(relevantStackFrames(groups[1]?.frames ?? [])).toHaveLength(2);
  });

  test("falls back to the single crashed thread stack", () => {
    const groups = eventStackGroups(
      detail({
        threads: {
          values: [
            {
              current: true,
              id: 1,
              stacktrace: { frames: [{ filename: "current.rs" }] },
            },
            {
              crashed: true,
              id: 2,
              name: "crash-handler",
              stacktrace: { frames: [{ filename: "crashed.rs" }] },
            },
          ],
        },
      })
    );

    expect(groups).toHaveLength(1);
    expect(groups[0]?.label).toBe("Thread crash-handler");
    expect(groups[0]?.frames[0]?.filename).toBe("crashed.rs");
  });

  test("attaches a sole stackless exception to its crashed thread", () => {
    const groups = eventStackGroups(
      detail({
        exception: {
          values: [
            {
              mechanism: {
                handled: false,
                help_link: "https://docs.example.com/crash",
                meta: {
                  signal: { code_name: "SEGV_MAPERR", name: "SIGSEGV" },
                },
                type: "signalhandler",
              },
              type: "NativeCrash",
              value: "invalid address",
            },
          ],
        },
        threads: {
          values: [
            {
              crashed: true,
              id: 7,
              stacktrace: { frames: [{ filename: "crash.c" }] },
            },
          ],
        },
      })
    );

    expect(groups[0]?.label).toBe("NativeCrash: invalid address");
    expect(groups[0]?.frames[0]?.filename).toBe("crash.c");
    expect(groups[0]?.mechanism?.data).toContainEqual({
      name: "signal",
      value: "SIGSEGV (SEGV_MAPERR)",
    });
    expect(groups[0]?.mechanism?.helpLink).toBe(
      "https://docs.example.com/crash"
    );
  });

  test("uses the first thread with a stack when no thread crashed", () => {
    const groups = eventStackGroups(
      detail({
        threads: {
          values: [
            { current: true, id: 1 },
            {
              id: 2,
              name: "worker",
              stacktrace: { frames: [{ filename: "worker.rs" }] },
            },
          ],
        },
      })
    );

    expect(groups[0]?.label).toBe("Thread worker");
    expect(groups[0]?.frames[0]?.filename).toBe("worker.rs");
  });
});
