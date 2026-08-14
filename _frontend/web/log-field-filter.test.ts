import { expect, test } from "bun:test";

import { logFieldFiltersSchema, structuredLogFields } from "./log-field-filter";

test("validates bounded structured log filters", () => {
  expect(
    logFieldFiltersSchema.parse([
      { operator: "equals", path: "caller", value: "server.go:42" },
      { operator: "contains", path: "http.route", value: "checkout" },
      { operator: "exists", path: "request_id" },
    ])
  ).toHaveLength(3);
  expect(
    logFieldFiltersSchema.safeParse([
      { operator: "equals", path: "caller') OR 1=1", value: "x" },
    ]).success
  ).toBe(false);
});

test("flattens safe scalar fields without duplicating message metadata", () => {
  expect(
    structuredLogFields({
      caller: "server.go:42",
      http: { method: "GET", status_code: 503 },
      level: "error",
      message: "failed",
    })
  ).toEqual([
    { path: "caller", value: "server.go:42" },
    { path: "http.method", value: "GET" },
    { path: "http.status_code", value: "503" },
  ]);
});
