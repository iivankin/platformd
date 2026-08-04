import { expect, test } from "bun:test";

import type { Service } from "@/api";
import { emptyPendingBackupPolicy } from "@/pending-resource-creation";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import {
  emptyStoredProjectChanges,
  loadStoredProjectChanges,
  saveStoredProjectChanges,
} from "@/project-changes-storage";
import {
  createPendingServiceSettings,
  createServiceSettingsDraft,
} from "@/service-settings-model";

const memoryStorage = (initial: string | null = null) => {
  let value = initial;
  return {
    getItem: () => value,
    read: () => value,
    removeItem: () => {
      value = null;
    },
    setItem: (_key: string, next: string) => {
      value = next;
    },
  };
};

const service: Service = {
  createdAt: 1,
  enabled: true,
  environment: { LOG_LEVEL: "info" },
  id: "api",
  name: "api",
  projectId: "project",
  secretReferences: [],
  source: {
    autoUpdate: true,
    image: { reference: "docker.io/library/alpine:latest" },
    minimumReleaseAgeDays: 7,
    type: "public_image",
  },
  updatedAt: 2,
  volumeMounts: [],
};

test("round-trips everything required by the project change bar", () => {
  const change = createPendingServiceSettings({
    domains: [],
    draft: createServiceSettingsDraft(service, [], [], []),
    environment: { API_TOKEN: "secret" },
    listeners: [],
    service,
    volumes: [],
  });
  if (!change) {
    throw new Error("service change was not created");
  }
  const redis: PendingResourceCreation = {
    backupPolicy: emptyPendingBackupPolicy(),
    id: "draft:redis",
    input: {
      credentials: { password: "redis-secret" },
      imageTag: "8.2",
      name: "redis",
    },
    kind: "redis",
  };
  const stored = {
    changes: { project: { [service.id]: change } },
    resourceDrafts: { project: { [redis.id]: redis } },
  };
  const storage = memoryStorage();

  saveStoredProjectChanges(stored, storage);

  expect(loadStoredProjectChanges(storage)).toEqual(stored);
  saveStoredProjectChanges(emptyStoredProjectChanges(), storage);
  expect(storage.read()).toBeNull();
});

test("drops a stored payload that does not match the current schema", () => {
  const storage = memoryStorage(
    JSON.stringify({
      changes: { project: { api: { environment: "invalid" } } },
      resourceDrafts: {},
      version: 1,
    })
  );

  expect(loadStoredProjectChanges(storage)).toEqual(
    emptyStoredProjectChanges()
  );
  expect(storage.read()).toBeNull();
});
