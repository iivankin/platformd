import { expect, test } from "bun:test";

import {
  emptyServiceConfigurationDraft,
  parseServiceConfiguration,
  serviceConfigurationDraftFromCreateInput,
} from "@/service-configuration";

const previewDraft = () => ({
  ...emptyServiceConfigurationDraft(),
  source: {
    github: {
      branch: "main",
      contextPath: ".",
      dockerfilePath: "Dockerfile",
      pullRequestPreview: {
        hostnameTemplate: "preview-{{hash}}.example.com",
      },
      repository: "acme/api",
      repositoryId: 42,
      triggerPaths: [],
      waitForCi: true,
    },
    type: "github" as const,
  },
});

test("requires exactly one HTTP domain for pull request previews", () => {
  expect(() => parseServiceConfiguration(previewDraft(), 0)).toThrow(
    "PR previews require exactly one HTTP domain"
  );
  expect(() => parseServiceConfiguration(previewDraft(), 2)).toThrow(
    "PR previews require exactly one HTTP domain"
  );
  expect(parseServiceConfiguration(previewDraft(), 1).source.type).toBe(
    "github"
  );
});

test("allows an incomplete preview domain while a service is still a draft", () => {
  expect(parseServiceConfiguration(previewDraft()).source.type).toBe("github");
});

test("keeps private registry credentials in the service configuration", () => {
  const draft = emptyServiceConfigurationDraft();
  draft.source = {
    autoUpdate: true,
    image: { reference: "registry.example.com/team/api:latest" },
    type: "private_image",
  };
  draft.registryCredential = { password: "secret", username: "robot" };

  expect(parseServiceConfiguration(draft)).toMatchObject({
    registryCredential: { password: "secret", username: "robot" },
    source: {
      image: { reference: "registry.example.com/team/api:latest" },
      type: "private_image",
    },
  });
});

test("restores editable settings from a pending service creation", () => {
  const source = {
    autoUpdate: true,
    image: { reference: "registry.example.com/team/api:latest" },
    type: "private_image" as const,
  };

  expect(
    serviceConfigurationDraftFromCreateInput({
      environment: { LOG_LEVEL: "info" },
      healthCheck: { path: "/ready", port: 9090, timeoutSeconds: 15 },
      name: "api",
      registryCredential: { password: "secret", username: "robot" },
      source,
    })
  ).toEqual({
    healthEnabled: true,
    healthPath: "/ready",
    healthPort: "9090",
    healthTimeout: "15",
    registryCredential: { password: "secret", username: "robot" },
    source,
  });
});
