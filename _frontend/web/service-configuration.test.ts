import { expect, test } from "bun:test";

import {
  emptyServiceConfigurationDraft,
  parseServiceConfiguration,
  serviceConfigurationDraftFromCreateInput,
} from "@/service-configuration";

const uploadDraft = () => ({
  ...emptyServiceConfigurationDraft(),
  source: {
    dockerUpload: {
      branch: "main",
      repository: "acme/api",
      workflows: ["deploy.yml"],
    },
    type: "docker_image_upload" as const,
  },
});

test("requires exactly one HTTP domain for uploaded images", () => {
  expect(() => parseServiceConfiguration(uploadDraft(), 0)).toThrow(
    "Image upload services require exactly one HTTP domain"
  );
  expect(() => parseServiceConfiguration(uploadDraft(), 2)).toThrow(
    "Image upload services require exactly one HTTP domain"
  );
  expect(parseServiceConfiguration(uploadDraft(), 1).source.type).toBe(
    "docker_image_upload"
  );
});

test("allows an incomplete upload domain while a service is still a draft", () => {
  expect(parseServiceConfiguration(uploadDraft()).source.type).toBe(
    "docker_image_upload"
  );
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

test("keeps and validates the remote image minimum release age", () => {
  const draft = emptyServiceConfigurationDraft();
  draft.source = {
    autoUpdate: true,
    image: { reference: "docker.io/library/alpine:latest" },
    minimumReleaseAgeDays: 7,
    type: "public_image",
  };

  expect(parseServiceConfiguration(draft).source).toMatchObject({
    minimumReleaseAgeDays: 7,
  });

  draft.source.minimumReleaseAgeDays = 0;
  expect(() => parseServiceConfiguration(draft)).toThrow(
    "Minimum release age must be between 1 and 36500 days"
  );
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
