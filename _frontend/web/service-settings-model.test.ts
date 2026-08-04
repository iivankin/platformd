import { expect, test } from "bun:test";

import type { Service, ServiceDomain, ServiceListener, Volume } from "@/api";
import {
  createPendingServiceSettings,
  createServiceSettingsDraft,
  serviceSettingsChangeDetails,
} from "@/service-settings-model";

const service: Service = {
  createdAt: 1,
  enabled: true,
  environment: {},
  id: "service",
  name: "api",
  projectId: "project",
  secretReferences: [],
  source: {
    autoUpdate: true,
    image: { reference: "docker.io/example/api:stable" },
    type: "public_image",
  },
  updatedAt: 2,
  volumeMounts: [{ containerPath: "/data", volumeId: "volume" }],
};

const domains: ServiceDomain[] = [
  {
    createdAt: 1,
    hostname: "api.example.com",
    internalOutputName: "API_URL_INTERNAL",
    publicOutputName: "API_URL",
    serviceId: service.id,
    targetPort: 8080,
  },
];

const listeners: ServiceListener[] = [
  {
    createdAt: 1,
    protocol: "tcp",
    publicPort: 9000,
    serviceId: service.id,
    targetPort: 8080,
  },
];

const volumes: Volume[] = [
  {
    createdAt: 1,
    id: "volume",
    name: "data",
    projectId: service.projectId,
    serviceId: service.id,
  },
];

test("does not stage unchanged service settings", () => {
  const draft = createServiceSettingsDraft(
    service,
    domains,
    listeners,
    volumes
  );
  expect(
    createPendingServiceSettings({
      domains,
      draft,
      listeners,
      service,
      volumes,
    })
  ).toBeUndefined();
});

test("stages before-deploy actions as separate review details", () => {
  const draft = createServiceSettingsDraft(
    service,
    domains,
    listeners,
    volumes
  );
  draft.beforeDeploy = {
    ...draft.beforeDeploy,
    cloudflareEnabled: true,
    cloudflareHostnames: ["api.example.com"],
    command: "bun run migrate",
    commandEnabled: true,
  };
  const change = createPendingServiceSettings({
    domains,
    draft,
    listeners,
    service,
    volumes,
  });
  if (!change) {
    throw new Error("before-deploy change was not staged");
  }
  expect(
    serviceSettingsChangeDetails(change)
      .filter(({ id }) => id.startsWith("before-deploy-"))
      .map(({ id }) => id)
  ).toEqual(["before-deploy-command", "before-deploy-cloudflare"]);
});

test("keeps one baseline while accumulating service setting changes", () => {
  const draft = createServiceSettingsDraft(
    service,
    domains,
    listeners,
    volumes
  );
  const first = createPendingServiceSettings({
    domains,
    draft: {
      ...draft,
      domains: [{ hostname: "api.example.com", targetPort: 3000 }],
    },
    listeners,
    service,
    volumes,
  });
  if (!first) {
    throw new Error("first change was not staged");
  }
  const second = createPendingServiceSettings({
    current: first,
    domains,
    draft: {
      ...first.draft,
      volumeMounts: [],
    },
    listeners,
    service: { ...service, updatedAt: 99 },
    volumes,
  });
  if (!second) {
    throw new Error("second change was not staged");
  }
  expect(second.baseline.service.updatedAt).toBe(2);
  expect(
    serviceSettingsChangeDetails(second).map((change) => change.label)
  ).toEqual(["Domain port", "Unmount volume"]);
});

test("stages variable edits on the existing service baseline", () => {
  const serviceWithVariables = {
    ...service,
    environment: { LEGACY_TOKEN: "old", SHARED_URL: "https://old.example" },
  };
  const draft = createServiceSettingsDraft(
    serviceWithVariables,
    domains,
    listeners,
    volumes
  );
  const change = createPendingServiceSettings({
    domains,
    draft,
    environment: {
      API_TOKEN: "new",
      SHARED_URL: "https://new.example",
    },
    listeners,
    service: serviceWithVariables,
    volumes,
  });
  if (!change) {
    throw new Error("variable changes were not staged");
  }

  expect(change.baseline.service.environment).toEqual({
    LEGACY_TOKEN: "old",
    SHARED_URL: "https://old.example",
  });
  expect(change.environment).toEqual({
    API_TOKEN: "new",
    SHARED_URL: "https://new.example",
  });
  expect(
    serviceSettingsChangeDetails(change).map(({ detail, label }) => [
      label,
      detail,
    ])
  ).toEqual([
    ["Add variable", "API_TOKEN"],
    ["Remove variable", "LEGACY_TOKEN"],
    ["Update variable", "SHARED_URL"],
  ]);
});

test("counts a pending volume and a domain as separate changes", () => {
  const draft = createServiceSettingsDraft(
    service,
    domains,
    listeners,
    volumes
  );
  const change = createPendingServiceSettings({
    domains,
    draft: {
      ...draft,
      domains: [...draft.domains, { hostname: "mock.local", targetPort: 2020 }],
      volumes: [
        ...draft.volumes,
        {
          createdAt: 3,
          id: "pending-volume:new",
          name: "data2",
          pendingCreation: true,
          projectId: service.projectId,
          serviceId: service.id,
        },
      ],
    },
    listeners,
    service,
    volumes,
  });
  if (!change) {
    throw new Error("changes were not staged");
  }
  expect(
    serviceSettingsChangeDetails(change).map((detail) => detail.label)
  ).toEqual(["Add domain", "Add volume"]);
});
