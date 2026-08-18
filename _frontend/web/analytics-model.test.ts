import { expect, test } from "bun:test";

import {
  analyticsCookieDomain,
  analyticsEffectiveRollout,
  analyticsFlagSummary,
  analyticsNextRolloutStep,
  analyticsRampSteps,
  funnelReached,
  matchingHostnames,
  rootsOverlap,
  trackerRootCandidates,
} from "@/analytics-model";

const now = 1_700_000_000_000;

test("boolean flag summary uses rollout, not fake 50/50 weights", () => {
  const targeting = {
    groups: [
      {
        properties: [],
        rollout_percentage: 10,
        rollout_steps: [
          { at: now - 1000, percentage: 10 },
          { at: now + 86_400_000, percentage: 50 },
        ],
      },
    ],
  };
  expect(
    analyticsFlagSummary(
      {
        enabled: true,
        targeting,
        type: "boolean",
        variants: [
          { key: "false", percentage: 50 },
          { key: "true", percentage: 50 },
        ],
      },
      now
    )
  ).toContain("boolean · on · 10% → 50%");
  expect(analyticsEffectiveRollout(targeting, now)).toBe(10);
  expect(analyticsNextRolloutStep(targeting, now)?.percentage).toBe(50);
});

test("zero rollout stays off", () => {
  expect(
    analyticsEffectiveRollout({
      groups: [{ properties: [], rollout_percentage: 0, rollout_steps: [] }],
    })
  ).toBe(0);
});

test("empty targeting is a full rollout", () => {
  expect(analyticsEffectiveRollout({ groups: [] })).toBe(100);
  expect(analyticsRampSteps(now).map((step) => step.percentage)).toEqual([
    1, 10, 50, 100,
  ]);
});

test("localhost is a tracker root candidate", () => {
  expect(trackerRootCandidates(["localhost", "www.example.com"])).toEqual([
    "localhost",
    "example.com",
    "www.example.com",
  ]);
});

test("rootsOverlap and cookie domain follow Go host rules", () => {
  expect(rootsOverlap("Example.com.", "www.example.com")).toBe(true);
  expect(rootsOverlap("example.com:443", "www.example.com")).toBe(true);
  expect(rootsOverlap("Example.com.:443", "www.example.com")).toBe(true);
  expect(rootsOverlap("localhost", "localhost")).toBe(true);
  expect(rootsOverlap("", "example.com")).toBe(false);
  expect(
    matchingHostnames("Example.com.", ["WWW.example.com", "other.com"])
  ).toEqual(["WWW.example.com"]);
  expect(analyticsCookieDomain("shop.example")).toBe(".shop.example");
  expect(analyticsCookieDomain("shop.example:443")).toBe(".shop.example");
  expect(analyticsCookieDomain("localhost")).toBe("");
  expect(analyticsCookieDomain("127.0.0.1")).toBe("");
});

test("funnelReached treats rows as exact max-level counts", () => {
  expect(
    funnelReached(
      [
        { level: 1, visitors: 560 },
        { level: 2, visitors: 430 },
        { level: 3, visitors: 210 },
      ],
      3
    ).map((row) => row.reached)
  ).toEqual([1200, 640, 210]);
});
