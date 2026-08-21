import { expect, test } from "bun:test";

import {
  analyticsCountryName,
  analyticsLocationName,
} from "@/analytics-location";
import {
  analyticsBotPurpose,
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

test("analytics locations omit missing and duplicate parts", () => {
  expect(analyticsCountryName("RS")).toBe("Serbia");
  expect(analyticsLocationName({ city: "", country: "RS", region: "" })).toBe(
    "Serbia"
  );
  expect(
    analyticsLocationName({
      city: "Belgrade",
      country: "RS",
      region: "Belgrade",
    })
  ).toBe("Belgrade, Serbia");
  expect(analyticsLocationName({ city: "", country: "", region: "" })).toBe(
    "Unknown"
  );
});

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

test("bot purpose names the crawler job, not the bucket", () => {
  expect(analyticsBotPurpose("OAI-SearchBot", "search")).toBe(
    "ChatGPT indexed the page for future search answers"
  );
  expect(analyticsBotPurpose("GPTBot", "training")).toBe(
    "OpenAI fetched the page for possible model training"
  );
  expect(analyticsBotPurpose("ChatGPT-User", "fetch")).toBe(
    "A user asked ChatGPT or a Custom GPT a question, triggering a live page fetch"
  );
  expect(analyticsBotPurpose("Bytespider", "search")).toBe(
    "ByteDance indexed the page for search"
  );
  expect(analyticsBotPurpose("CCBot", "training")).toBe(
    "Common Crawl archived the page for its web dataset"
  );
  expect(analyticsBotPurpose("Google-Extended", "training")).toBe(
    "Google may use the page for Gemini grounding"
  );
  expect(analyticsBotPurpose("UnknownBot", "search")).toBe(
    "A crawler indexed the page for future search answers"
  );
});
