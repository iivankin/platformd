import { expect, test } from "bun:test";

import { createGitHubWebhookSecret } from "@/github-webhook-secret";

test("generates a strong GitHub webhook secret safe for copy and paste", () => {
  const first = createGitHubWebhookSecret();
  const second = createGitHubWebhookSecret();

  expect(first).toMatch(/^[\w-]{43}$/u);
  expect(second).toMatch(/^[\w-]{43}$/u);
  expect(second).not.toBe(first);
});
