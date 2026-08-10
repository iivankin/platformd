import { describe, expect, test } from "bun:test";

import {
  eventBreadcrumbs,
  eventContextGroups,
  eventTags,
} from "./event-context";

describe("Sentry event context", () => {
  test("normalizes protocol variants used by SDKs", () => {
    const payload = {
      breadcrumbs: {
        values: [
          {
            category: "fetch",
            data: { status_code: 500 },
            message: "request failed",
            timestamp: 123,
            type: "http",
          },
        ],
      },
      contexts: {
        browser: { name: "Chrome", type: "browser", version: "140" },
      },
      extra: { attempt: 3 },
      request: { method: "POST", url: "/checkout" },
      tags: [["region", "eu"], { key: "replayId", value: "replay" }],
      user: { id: "customer" },
    };

    expect(eventBreadcrumbs(payload)).toMatchObject([
      { category: "fetch", message: "request failed", type: "http" },
    ]);
    expect(eventTags(payload)).toEqual([
      ["region", "eu"],
      ["replayId", "replay"],
    ]);
    expect(eventContextGroups(payload).map(([name]) => name)).toEqual([
      "User",
      "Request",
      "browser",
      "Extra",
    ]);
  });
});
