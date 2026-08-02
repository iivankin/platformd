import { expect, test } from "bun:test";

import type { Service, ServiceDomain, ServiceListener, Volume } from "@/api";
import { assertServiceSettingsBaseline } from "@/service-settings-apply";
import {
  createPendingServiceSettings,
  createServiceSettingsDraft,
} from "@/service-settings-model";

const service: Service = {
  buildEnvironment: {},
  createdAt: 1,
  enabled: true,
  environment: {},
  id: "api",
  name: "api",
  projectId: "project",
  secretReferences: [],
  source: {
    autoUpdate: true,
    image: { reference: "docker.io/library/alpine:latest" },
    type: "public_image",
  },
  updatedAt: 2,
  volumeMounts: [],
};
const domain: ServiceDomain = {
  createdAt: 1,
  hostname: "api.example.com",
  internalOutputName: "API_URL_INTERNAL",
  publicOutputName: "API_URL",
  serviceId: service.id,
  targetPort: 8080,
};
const listener: ServiceListener = {
  createdAt: 1,
  protocol: "tcp",
  publicPort: 5432,
  serviceId: service.id,
  targetPort: 5432,
};
const volume: Volume = {
  createdAt: 1,
  id: "volume",
  name: "data",
  projectId: "project",
  serviceId: service.id,
};
const change = createPendingServiceSettings({
  domains: [domain],
  draft: createServiceSettingsDraft(service, [domain], [listener], [volume]),
  environment: { API_TOKEN: "secret" },
  listeners: [listener],
  service,
  volumes: [volume],
});
if (!change) {
  throw new Error("service change was not created");
}

test("accepts the staged baseline and retry-created volumes", () => {
  expect(() =>
    assertServiceSettingsBaseline(
      change,
      service,
      [domain],
      [listener],
      [
        volume,
        {
          createdAt: 2,
          id: "retry-volume",
          name: "uploads",
          projectId: "project",
          serviceId: service.id,
        },
      ]
    )
  ).not.toThrow();
});

test("rejects backend changes made after the draft was staged", () => {
  const staleCases = [
    {
      domains: [domain],
      listeners: [listener],
      service: { ...service, updatedAt: 3 },
      volumes: [volume],
    },
    {
      domains: [{ ...domain, targetPort: 3000 }],
      listeners: [listener],
      service,
      volumes: [volume],
    },
    {
      domains: [domain],
      listeners: [{ ...listener, targetPort: 6432 }],
      service,
      volumes: [volume],
    },
    {
      domains: [domain],
      listeners: [listener],
      service,
      volumes: [],
    },
  ];

  for (const current of staleCases) {
    expect(() =>
      assertServiceSettingsBaseline(
        change,
        current.service,
        current.domains,
        current.listeners,
        current.volumes
      )
    ).toThrow("Service settings changed after this draft was staged");
  }
});
