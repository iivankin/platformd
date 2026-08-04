import { expect, test } from "bun:test";

import {
  emptyBeforeDeployDraft,
  parseBeforeDeploy,
} from "@/service-before-deploy-model";

test("parses command and Cloudflare purge actions", () => {
  const configuration = parseBeforeDeploy(
    {
      ...emptyBeforeDeployDraft(),
      cloudflareEnabled: true,
      cloudflareHostnames: ["api.example.com"],
      command: "  bun run migrate  ",
      commandEnabled: true,
    },
    [{ hostname: "api.example.com" }]
  );
  expect(configuration).toEqual({
    cloudflareHostnames: ["api.example.com"],
    command: "bun run migrate",
  });
});

test("rejects Cloudflare hostnames not attached to the service", () => {
  expect(() =>
    parseBeforeDeploy(
      {
        ...emptyBeforeDeployDraft(),
        cloudflareEnabled: true,
        cloudflareHostnames: ["other.example.com"],
      },
      [{ hostname: "api.example.com" }]
    )
  ).toThrow("attached to the service");
});
