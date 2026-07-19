import { expect, test } from "bun:test";

import { addTriggerPath, isTriggerPathCovered } from "@/trigger-path-model";

test("treats a selected path as covering itself and its descendants", () => {
  expect(isTriggerPathCovered("apps", ["apps"])).toBe(true);
  expect(isTriggerPathCovered("apps/api", ["apps"])).toBe(true);
  expect(isTriggerPathCovered("apps/api/routes.ts", ["apps/api"])).toBe(true);
  expect(isTriggerPathCovered("packages/shared", ["apps"])).toBe(false);
  expect(isTriggerPathCovered("application", ["app"])).toBe(false);
});

test("does not add a path already covered by a selected parent", () => {
  expect(addTriggerPath(["apps"], "apps/api")).toEqual(["apps"]);
});

test("replaces selected descendants when a parent path is added", () => {
  expect(
    addTriggerPath(["apps/api", "packages/shared", "apps/web"], "/apps/")
  ).toEqual(["packages/shared", "apps"]);
});
