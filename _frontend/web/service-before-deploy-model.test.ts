import { expect, test } from "bun:test";

import type { ServiceSource } from "@/api";
import {
  emptyBeforeDeployDraft,
  parseBeforeDeploy,
} from "@/service-before-deploy-model";

const githubSource: ServiceSource = {
  github: {
    branch: "main",
    contextPath: ".",
    dockerfilePath: "Dockerfile",
    repository: "acme/api",
    repositoryId: 23,
    triggerPaths: [],
    waitForCi: false,
  },
  type: "github",
};

test("parses all before-deploy actions and arbitrary JSON workflow inputs", () => {
  const configuration = parseBeforeDeploy(
    {
      ...emptyBeforeDeployDraft(),
      cloudflareEnabled: true,
      cloudflareHostnames: ["api.example.com"],
      command: "  bun run migrate  ",
      commandEnabled: true,
      githubInputs:
        '{"dryRun":false,"batch":20,"metadata":{"actor":"platformd"}}',
      githubWorkflow: {
        inputs: {},
        name: "Migrate database",
        path: ".github/workflows/migrate.yml",
      },
      githubWorkflowEnabled: true,
    },
    githubSource,
    [{ hostname: "api.example.com" }]
  );
  expect(configuration).toEqual({
    cloudflareHostnames: ["api.example.com"],
    command: "bun run migrate",
    githubWorkflow: {
      inputs: {
        batch: 20,
        dryRun: false,
        metadata: { actor: "platformd" },
      },
      name: "Migrate database",
      path: ".github/workflows/migrate.yml",
    },
  });
});

test("rejects non-object workflow inputs", () => {
  expect(() =>
    parseBeforeDeploy(
      {
        ...emptyBeforeDeployDraft(),
        githubInputs: '["not", "an", "object"]',
        githubWorkflow: {
          inputs: {},
          name: "Migrate",
          path: ".github/workflows/migrate.yml",
        },
        githubWorkflowEnabled: true,
      },
      githubSource,
      []
    )
  ).toThrow("JSON object");
});

test("rejects Cloudflare hostnames not attached to the service", () => {
  expect(() =>
    parseBeforeDeploy(
      {
        ...emptyBeforeDeployDraft(),
        cloudflareEnabled: true,
        cloudflareHostnames: ["other.example.com"],
      },
      githubSource,
      [{ hostname: "api.example.com" }]
    )
  ).toThrow("attached to the service");
});
