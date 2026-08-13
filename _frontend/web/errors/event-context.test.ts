import { describe, expect, test } from "bun:test";

import {
  eventBreadcrumbs,
  eventContextGroups,
  eventEnvironment,
  eventRequest,
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
        trace: { trace_id: "trace" },
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
      "trace",
      "Extra",
    ]);
  });

  test("builds dedicated identity and runtime summaries", () => {
    const payload = {
      contexts: {
        browser: { name: "Chrome", type: "browser", version: "140" },
        device: { brand: "Apple", family: "Mac", model: "MacBookPro18,3" },
        os: { build: "24G90", name: "macOS", version: "15.6" },
        runtime: { name: "Bun", version: "1.2.20" },
      },
      user: {
        email: "alex@example.com",
        geo: { city: "Belgrade", country_code: "RS", region: "Belgrade" },
        id: "customer_1042",
        ip_address: "203.0.113.42",
        username: "alex",
      },
    };

    expect(eventEnvironment(payload)).toEqual([
      {
        details: [
          ["Username", "alex"],
          ["Email", "alex@example.com"],
          ["ID", "customer_1042"],
          ["IP address", "203.0.113.42"],
        ],
        kind: "user",
        title: "alex",
      },
      {
        details: [],
        kind: "browser",
        title: "Chrome 140",
      },
      {
        details: [
          ["Brand", "Apple"],
          ["Family", "Mac"],
        ],
        kind: "device",
        title: "MacBookPro18,3",
      },
      {
        details: [["build", "24G90"]],
        kind: "os",
        title: "macOS 15.6",
      },
      {
        details: [],
        kind: "runtime",
        title: "Bun 1.2.20",
      },
      {
        details: [
          ["City", "Belgrade"],
          ["Region", "Belgrade"],
          ["Country", "RS"],
        ],
        kind: "geo",
        title: "Belgrade, RS",
      },
    ]);
  });

  test("normalizes request header representations", () => {
    expect(
      eventRequest({
        request: {
          headers: [
            ["User-Agent", "Sentry SDK"],
            { name: "Referer", value: "https://example.com/cart" },
          ],
          method: "post",
          query_string: "step=payment",
          url: "https://example.com/checkout",
        },
      })
    ).toEqual({
      headers: [
        ["User-Agent", "Sentry SDK"],
        ["Referer", "https://example.com/cart"],
      ],
      method: "POST",
      rows: [["query_string", "step=payment"]],
      url: "https://example.com/checkout",
    });
  });
});
