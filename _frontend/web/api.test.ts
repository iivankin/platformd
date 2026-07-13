import { expect, test } from "bun:test";

import {
  APIError,
  attachServiceDomain,
  createAPIToken,
  createObjectStore,
  createManagedRedis,
  createProject,
  createImageCredential,
  createManagedPostgres,
  createRegistryRepository,
  createRegistryCredential,
  cleanupRegistryRepository,
  createService,
  createVolume,
  detachServiceDomain,
  deleteObject,
  deleteBackupTarget,
  deleteRegistryImage,
  deleteRegistryCredential,
  deleteRegistryRepository,
  deleteRegistryTag,
  deleteVolume,
  fetchAPITokens,
  fetchAuditEvents,
  fetchBackupHistory,
  fetchBackupPolicy,
  fetchBackupPolicies,
  fetchBackupTarget,
  fetchBackupGenerations,
  fetchService,
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchServiceLogs,
  fetchServiceTerminalShells,
  issueServerTerminalToken,
  fetchVolumeOwnerSuggestion,
  fetchVolumes,
  fetchImageCredentials,
  fetchIdentity,
  fetchInfrastructureLogs,
  fetchDiskPressure,
  applySelfUpdate,
  fetchManagedImageTags,
  previewDatabaseVersion,
  startDatabaseVersionChange,
  fetchDatabaseVersionOperation,
  fetchManagedPostgres,
  fetchManagedRedis,
  fetchManagedRedisPersistence,
  fetchMeta,
  fetchOperation,
  fetchObjects,
  fetchObjectStore,
  fetchProjectCanvas,
  fetchProjects,
  fetchRegistryImage,
  fetchRegistryImages,
  fetchRegistryCredentials,
  fetchRegistryRepositories,
  fetchRegistrySettings,
  fetchRecoveryStatus,
  fetchResourceUsage,
  mutateManagedRedis,
  objectDownloadURL,
  previewObject,
  previewManagedRedisKey,
  queryManagedPostgres,
  redeployService,
  revokeAPIToken,
  rollbackService,
  runBackupNow,
  scanManagedRedisKeys,
  setRegistryHostname,
  setRegistryRepositoryPublicPull,
  setBackupTarget,
  setBackupPolicy,
  restoreBackupGeneration,
  retryRecovery,
  updateService,
  uploadObject,
} from "@/api";

const invalidMetaFetcher = () =>
  Promise.resolve(Response.json({ status: "healthy", version: 1 }));

const validMetaFetcher = () =>
  Promise.resolve(
    Response.json({
      architecture: "amd64",
      os: "linux",
      status: "ready",
      version: "0.1.0",
    })
  );

test("rejects a malformed control-plane metadata response", async () => {
  await expect(fetchMeta(undefined, invalidMetaFetcher)).rejects.toThrow();
});

test("starts a verified self-update through the dedicated idle-only endpoint", async () => {
  const calls: { input: RequestInfo | URL; init?: RequestInit }[] = [];
  const result = await applySelfUpdate((input, init) => {
    calls.push({ init, input });
    return Promise.resolve(
      Response.json(
        { previousVersion: "1.0.0", targetVersion: "2.0.0" },
        { status: 202 }
      )
    );
  });
  expect(result.targetVersion).toBe("2.0.0");
  expect(calls).toEqual([
    {
      init: { headers: { Accept: "application/json" }, method: "POST" },
      input: "/api/v1/infrastructure/update",
    },
  ]);
});

test("returns validated control-plane metadata", async () => {
  await expect(fetchMeta(undefined, validMetaFetcher)).resolves.toEqual({
    architecture: "amd64",
    os: "linux",
    status: "ready",
    version: "0.1.0",
  });
});

test("returns the validated Cloudflare Access identity", async () => {
  await expect(
    fetchIdentity(undefined, () =>
      Promise.resolve(
        Response.json({ email: "admin@example.com", subject: "access-user" })
      )
    )
  ).resolves.toEqual({ email: "admin@example.com", subject: "access-user" });
});

const project = {
  createdAt: 1,
  id: "project-id",
  name: "shop",
  objectStoreCount: 0,
  postgresCount: 0,
  redisCount: 0,
  serviceCount: 0,
  updatedAt: 1,
};

test("validates project list responses", async () => {
  await expect(
    fetchProjects(undefined, () => Promise.resolve(Response.json([project])))
  ).resolves.toEqual([project]);
  await expect(
    fetchProjects(undefined, () =>
      Promise.resolve(Response.json([{ ...project, serviceCount: -1 }]))
    )
  ).rejects.toThrow();
});

test("creates a project with the exact JSON contract", async () => {
  let requestInit: RequestInit | undefined;
  const created = await createProject("shop", (_input, init) => {
    requestInit = init;
    return Promise.resolve(Response.json(project, { status: 201 }));
  });
  expect(created).toEqual(project);
  expect(requestInit?.method).toBe("POST");
  expect(requestInit?.body).toBe('{"name":"shop"}');
});

test("surfaces structured API errors", async () => {
  await expect(
    createProject("shop", () =>
      Promise.resolve(
        Response.json(
          {
            error: { code: "project_name_conflict", message: "Already exists" },
          },
          { status: 409 }
        )
      )
    )
  ).rejects.toThrow("Already exists");
});

test("validates the project canvas and encodes its project ID", async () => {
  let requestURL = "";
  const canvas = await fetchProjectCanvas(
    "project/with slash",
    undefined,
    (input) => {
      requestURL = input.toString();
      return Promise.resolve(
        Response.json({
          connections: [
            {
              environmentNames: ["DATABASE_URL"],
              sourceId: "api",
              targetId: "database",
            },
          ],
          project,
          resources: [
            {
              enabled: true,
              id: "api",
              imageReference: "example/api:latest",
              internalHostname: "api.shop.internal",
              kind: "service",
              name: "api",
              status: "running",
            },
          ],
        })
      );
    }
  );
  expect(requestURL).toBe("/api/v1/projects/project%2Fwith%20slash/canvas");
  expect(canvas.connections[0]?.environmentNames).toEqual(["DATABASE_URL"]);
});

test("creates remote image credentials without changing the JSON fields", async () => {
  let requestInit: RequestInit | undefined;
  const credential = await createImageCredential(
    "project",
    {
      name: "production",
      password: "secret",
      registryHost: "registry.example.com",
      username: "robot",
    },
    (_input, init) => {
      requestInit = init;
      return Promise.resolve(
        Response.json(
          {
            createdAt: 1,
            id: "credential",
            name: "production",
            registryHost: "registry.example.com",
            username: "robot",
          },
          { status: 201 }
        )
      );
    }
  );
  expect(credential.id).toBe("credential");
  expect(requestInit?.body).toBe(
    '{"name":"production","password":"secret","registryHost":"registry.example.com","username":"robot"}'
  );
});

test("lists credentials and creates a service", async () => {
  const credential = {
    createdAt: 1,
    id: "credential",
    name: "production",
    registryHost: "registry.example.com",
    username: "robot",
  };
  await expect(
    fetchImageCredentials("project", undefined, () =>
      Promise.resolve(Response.json([credential]))
    )
  ).resolves.toEqual([credential]);
  let requestInit: RequestInit | undefined;
  const service = await createService(
    "project",
    {
      environment: { APP_ENV: "production" },
      imageCredentialId: "credential",
      imageReference: "registry.example.com/acme/api:latest",
      name: "api",
      targetPort: 8080,
    },
    (_input, init) => {
      requestInit = init;
      return Promise.resolve(
        Response.json(
          {
            createdAt: 1,
            enabled: true,
            environment: { APP_ENV: "production" },
            id: "service",
            imageCredentialId: "credential",
            imageReference: "registry.example.com/acme/api:latest",
            name: "api",
            projectId: "project",
            secretReferences: [],
            startupTimeoutSeconds: 60,
            targetPort: 8080,
            updatedAt: 1,
            volumeMounts: [],
          },
          { status: 201 }
        )
      );
    }
  );
  expect(service.id).toBe("service");
  expect(requestInit?.method).toBe("POST");
});

test("reads and mutates service lifecycle with optimistic version fields", async () => {
  const service = {
    createdAt: 1,
    enabled: true,
    environment: {},
    id: "service",
    imageReference: "docker.io/library/alpine:latest",
    name: "api",
    projectId: "project",
    secretReferences: [],
    startupTimeoutSeconds: 60,
    updatedAt: 2,
    volumeMounts: [],
  };
  await expect(
    fetchService("project", "service", undefined, () =>
      Promise.resolve(Response.json(service))
    )
  ).resolves.toEqual(service);

  let updateBody = "";
  await updateService(
    "project",
    "service",
    {
      enabled: false,
      environment: {},
      expectedUpdatedAt: 2,
      imageReference: service.imageReference,
      secretReferences: [],
      startupTimeoutSeconds: 60,
      volumeMounts: [],
    },
    (_input, init) => {
      updateBody = init?.body?.toString() ?? "";
      return Promise.resolve(
        Response.json({ ...service, enabled: false, updatedAt: 3 })
      );
    }
  );
  expect(JSON.parse(updateBody)).toEqual({
    enabled: false,
    environment: {},
    expectedUpdatedAt: 2,
    imageReference: service.imageReference,
    secretReferences: [],
    startupTimeoutSeconds: 60,
    volumeMounts: [],
  });

  await expect(
    redeployService("project", "service", 2, () =>
      Promise.resolve(Response.json(service))
    )
  ).resolves.toEqual(service);
  await expect(
    rollbackService("project", "service", "deployment", 2, () =>
      Promise.resolve(Response.json({ ...service, updatedAt: 3 }))
    )
  ).resolves.toMatchObject({ updatedAt: 3 });
});

test("manages service-owned volumes and reads the image owner suggestion", async () => {
  const item = {
    createdAt: 1,
    id: "volume/id",
    name: "data",
    ownerGid: 1001,
    ownerUid: 1000,
    projectId: "project/id",
    serviceId: "service/id",
  };
  await expect(
    fetchVolumes("project/id", "service/id", undefined, (input) => {
      expect(input.toString()).toBe(
        "/api/v1/projects/project%2Fid/services/service%2Fid/volumes"
      );
      return Promise.resolve(Response.json([item]));
    })
  ).resolves.toEqual([item]);
  await expect(
    fetchVolumeOwnerSuggestion(
      "project/id",
      "service/id",
      undefined,
      (input) => {
        expect(input.toString()).toEndWith("/volumes/owner-suggestion");
        return Promise.resolve(
          Response.json({
            exactNumeric: true,
            imageUser: "1000:1001",
            ownerGid: 1001,
            ownerUid: 1000,
          })
        );
      }
    )
  ).resolves.toMatchObject({ exactNumeric: true, ownerUid: 1000 });
  await expect(
    createVolume(
      "project/id",
      "service/id",
      { name: "data", ownerGid: 1001, ownerUid: 1000 },
      (_input, init) => {
        expect(init?.method).toBe("POST");
        expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({
          name: "data",
          ownerGid: 1001,
          ownerUid: 1000,
        });
        return Promise.resolve(Response.json(item, { status: 201 }));
      }
    )
  ).resolves.toEqual(item);
  await expect(
    deleteVolume("project/id", "service/id", "volume/id", (input, init) => {
      expect(input.toString()).toEndWith("/volumes/volume%2Fid");
      expect(init?.method).toBe("DELETE");
      return Promise.resolve(new Response(null, { status: 204 }));
    })
  ).resolves.toBeUndefined();
});

test("validates bounded deployment history pages", async () => {
  await expect(
    fetchServiceDeployments("project", "service", undefined, undefined, () =>
      Promise.resolve(
        Response.json({
          deployments: [
            {
              createdAt: 1,
              id: "deployment",
              imageDigest: "sha256:image",
              serviceConfigHash: "config",
              serviceId: "service",
              snapshot: {
                environment: {},
                imageReference: "docker.io/library/alpine:latest",
                secretReferences: [],
                startupTimeoutSeconds: 60,
                volumeMounts: [],
              },
              status: "succeeded",
            },
          ],
          nextCursor: "deployment",
        })
      )
    )
  ).resolves.toMatchObject({ nextCursor: "deployment" });
});

test("reads a validated bounded structured log window", async () => {
  let requested = "";
  await expect(
    fetchServiceLogs(
      "project",
      "service",
      { contains: "ready", deploymentId: "deployment", limit: 25 },
      undefined,
      (input) => {
        requested = input.toString();
        return Promise.resolve(
          Response.json({
            records: [
              {
                attemptId: "attempt",
                deploymentId: "deployment",
                stream: "stdout",
                text: "ready",
                timestamp: "2026-07-12T10:00:00.000000001Z",
              },
            ],
            truncated: false,
          })
        );
      }
    )
  ).resolves.toMatchObject({ records: [{ text: "ready" }] });
  expect(requested).toBe(
    "/api/v1/projects/project/services/service/logs?limit=25&deploymentId=deployment&contains=ready"
  );
});

test("discovers only allowlisted shells in the running service", async () => {
  let requested = "";
  await expect(
    fetchServiceTerminalShells("project", "service", undefined, (input) => {
      requested = input.toString();
      return Promise.resolve(Response.json({ shells: ["/bin/sh"] }));
    })
  ).resolves.toEqual(["/bin/sh"]);
  expect(requested).toBe(
    "/api/v1/projects/project/services/service/terminal/shells"
  );
});

test("authorizes a server terminal without placing its bearer in the URL", async () => {
  let requested = "";
  let requestInit: RequestInit | undefined;
  await expect(
    issueServerTerminalToken("correct horse", (input, init) => {
      requested = input.toString();
      requestInit = init;
      return Promise.resolve(
        Response.json({ expiresAt: 1_900_000_030_000, token: "signed-token" })
      );
    })
  ).resolves.toEqual({
    expiresAt: 1_900_000_030_000,
    token: "signed-token",
  });
  expect(requested).toBe("/api/v1/server/terminal-token");
  expect(requestInit?.method).toBe("POST");
  expect(requestInit?.body).toBe('{"passphrase":"correct horse"}');
});

test("reads derived disk pressure without a persisted operation", async () => {
  await expect(
    fetchDiskPressure(undefined, () =>
      Promise.resolve(
        Response.json({
          availableBytes: 4,
          availableInodes: 500,
          byteBasisPoints: 9600,
          checkedAt: 42,
          inodeBasisPoints: 5000,
          level: "critical",
          reservePresent: false,
          totalBytes: 100,
          totalInodes: 1000,
        })
      )
    )
  ).resolves.toMatchObject({ level: "critical", reservePresent: false });
});

test("reads a bounded platform journald window", async () => {
  let requested = "";
  await expect(
    fetchInfrastructureLogs(25, undefined, (input) => {
      requested = input.toString();
      return Promise.resolve(
        Response.json({
          records: [
            {
              cursor: "cursor",
              identifier: "platformd",
              message: "ready",
              pid: "42",
              priority: 6,
              timestamp: "2026-07-12T10:00:00Z",
            },
          ],
          truncated: false,
        })
      );
    })
  ).resolves.toMatchObject({ records: [{ message: "ready" }] });
  expect(requested).toBe("/api/v1/infrastructure/logs?limit=25");
});

test("reads stateless resource cgroup usage", async () => {
  let requested = "";
  await expect(
    fetchResourceUsage("service", "api/id", undefined, (input) => {
      requested = input.toString();
      return Promise.resolve(
        Response.json({
          cpuUsageMicros: 123_456,
          hostCpuCores: 8,
          hostMemoryBytes: 16 * 1024 ** 3,
          memoryBytes: 64 * 1024 ** 2,
          observedAt: 42,
          running: true,
        })
      );
    })
  ).resolves.toMatchObject({ memoryBytes: 64 * 1024 ** 2, running: true });
  expect(requested).toBe(
    "/api/v1/infrastructure/resources/service/api%2Fid/usage"
  );
});

test("reads filtered paginated audit history", async () => {
  let requested = "";
  await expect(
    fetchAuditEvents(
      {
        action: "server.exec",
        actorKind: "token",
        cursor: "cursor",
        limit: 25,
        result: "succeeded",
      },
      undefined,
      (input) => {
        requested = input.toString();
        return Promise.resolve(
          Response.json({
            events: [
              {
                action: "server.exec",
                actorId: "token",
                actorKind: "token",
                createdAt: 20,
                id: "event",
                metadata: { durationMillis: 10 },
                result: "succeeded",
                targetId: "host",
                targetKind: "server",
              },
            ],
          })
        );
      }
    )
  ).resolves.toMatchObject({ events: [{ action: "server.exec" }] });
  expect(requested).toBe(
    "/api/v1/audit?limit=25&action=server.exec&actorKind=token&cursor=cursor&result=succeeded"
  );
});

test("reads one official managed image tag page", async () => {
  let requested = "";
  await expect(
    fetchManagedImageTags(
      "postgres",
      { page: 2, pageSize: 25, search: "18" },
      undefined,
      (input) => {
        requested = input.toString();
        return Promise.resolve(
          Response.json({
            page: 2,
            pageSize: 25,
            previousPage: 1,
            tags: [
              {
                lastUpdated: "2026-06-01T00:00:00Z",
                name: "18.3",
                platforms: [
                  {
                    architecture: "amd64",
                    digest: "sha256:image",
                    os: "linux",
                    sizeBytes: 42,
                  },
                ],
              },
            ],
            total: 100,
          })
        );
      }
    )
  ).resolves.toMatchObject({ tags: [{ name: "18.3" }] });
  expect(requested).toBe(
    "/api/v1/managed-images/postgres/tags?page=2&pageSize=25&search=18"
  );
});

test("previews, starts, and reads a stateless managed database version change", async () => {
  const preview = {
    availableFreeBytes: 300,
    currentDataBytes: 100,
    ready: true,
    requiredFreeBytes: 120,
    sourceDigest: "sha256:source",
    sourceTag: "17",
    targetDigest: "sha256:target",
    targetTag: "18",
  };
  await expect(
    previewDatabaseVersion(
      "postgres",
      "project/id",
      "postgres/id",
      "18",
      (input, init) => {
        expect(input.toString()).toBe(
          "/api/v1/projects/project%2Fid/postgres/postgres%2Fid/version-change/preview"
        );
        expect(init?.method).toBe("POST");
        expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({
          imageTag: "18",
        });
        return Promise.resolve(Response.json(preview));
      }
    )
  ).resolves.toEqual(preview);

  const operation = {
    id: "operation/id",
    kind: "postgres_version_change",
    progress: "resolved_target",
    startedAt: 1,
    status: "running" as const,
    targetId: "postgres/id",
  };
  await expect(
    startDatabaseVersionChange(
      "postgres",
      "project/id",
      "postgres/id",
      "18",
      "sha256:target",
      (input, init) => {
        expect(input.toString()).toBe(
          "/api/v1/projects/project%2Fid/postgres/postgres%2Fid/version-change"
        );
        expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({
          expectedTargetDigest: "sha256:target",
          imageTag: "18",
        });
        return Promise.resolve(
          Response.json(
            {
              operation,
              sourceDigest: preview.sourceDigest,
              sourceTag: preview.sourceTag,
              targetDigest: preview.targetDigest,
              targetTag: preview.targetTag,
            },
            { status: 202 }
          )
        );
      }
    )
  ).resolves.toMatchObject({ operation });

  await expect(
    fetchDatabaseVersionOperation(
      "postgres",
      "project/id",
      "postgres/id",
      "operation/id",
      undefined,
      (input) => {
        expect(input.toString()).toBe(
          "/api/v1/projects/project%2Fid/postgres/postgres%2Fid/version-change/operation%2Fid"
        );
        return Promise.resolve(Response.json(operation));
      }
    )
  ).resolves.toEqual(operation);
});

test("uses Access-only managed Redis data routes with encoded values unchanged", async () => {
  const resource = {
    backupEnabled: false,
    backupRetentionCount: 7,
    createdAt: 1,
    hostname: "cache.shop.internal",
    id: "redis/id",
    imageDigest: "sha256:redis",
    imageTag: "7.4",
    name: "cache",
    port: 6379 as const,
    projectId: "project/id",
    updatedAt: 1,
  };
  await expect(
    createManagedRedis(
      resource.projectId,
      { imageTag: resource.imageTag, name: resource.name },
      (_input, init) => {
        expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({
          imageTag: "7.4",
          name: "cache",
        });
        return Promise.resolve(Response.json(resource, { status: 201 }));
      }
    )
  ).resolves.toEqual(resource);
  await expect(
    fetchManagedRedis(resource.projectId, resource.id, undefined, (input) => {
      expect(input.toString()).toBe(
        "/api/v1/projects/project%2Fid/redis/redis%2Fid"
      );
      return Promise.resolve(Response.json(resource));
    })
  ).resolves.toEqual(resource);
  await expect(
    fetchManagedRedisPersistence(
      resource.projectId,
      resource.id,
      undefined,
      (input) => {
        expect(input.toString()).toBe(
          "/api/v1/projects/project%2Fid/redis/redis%2Fid/persistence"
        );
        return Promise.resolve(
          Response.json({
            actualRpoMillis: 600_000,
            backgroundSaveInProgress: false,
            lastBackgroundSaveSuccessful: true,
            lastSuccessfulSaveAt: 1_700_000_000_000,
            needsAttention: true,
            observedAt: 1_700_000_600_000,
            targetRpoMillis: 300_000,
          })
        );
      }
    )
  ).resolves.toMatchObject({ actualRpoMillis: 600_000, needsAttention: true });

  await expect(
    scanManagedRedisKeys(
      resource.projectId,
      resource.id,
      { count: 25, cursor: "9", match: "user:*" },
      undefined,
      (input) => {
        expect(input.toString()).toBe(
          "/api/v1/projects/project%2Fid/redis/redis%2Fid/keys?count=25&cursor=9&match=user%3A*"
        );
        return Promise.resolve(
          Response.json({
            keys: [
              {
                keyBase64: "dXNlcjox",
                keyText: "user:1",
                sizeBytes: 32,
                type: "hash",
              },
            ],
            nextCursor: "0",
          })
        );
      }
    )
  ).resolves.toMatchObject({ keys: [{ type: "hash" }] });

  await expect(
    previewManagedRedisKey(
      resource.projectId,
      resource.id,
      "dXNlcjox",
      undefined,
      (input) => {
        expect(input.toString()).toContain("/preview?count=20&key=dXNlcjox");
        return Promise.resolve(
          Response.json({
            items: [
              {
                values: [
                  { base64: "bmFtZQ", text: "name" },
                  { base64: "QWRh", text: "Ada" },
                ],
              },
            ],
            length: 1,
            nextCursor: "0",
            truncated: false,
            type: "hash",
          })
        );
      }
    )
  ).resolves.toMatchObject({ length: 1, type: "hash" });

  await expect(
    mutateManagedRedis(
      resource.projectId,
      resource.id,
      {
        field: "bmFtZQ",
        key: "dXNlcjox",
        operation: "hash_set",
        value: "QWRh",
      },
      (_input, init) => {
        expect(init?.method).toBe("POST");
        expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({
          field: "bmFtZQ",
          key: "dXNlcjox",
          operation: "hash_set",
          value: "QWRh",
        });
        return Promise.resolve(
          Response.json({ affected: 1, auditRecorded: true, streamId: "" })
        );
      }
    )
  ).resolves.toEqual({ affected: 1, auditRecorded: true, streamId: "" });
});

test("creates PostgreSQL and runs bounded SQL only through the admin client", async () => {
  const resource = {
    backupEnabled: false,
    backupRetentionCount: 7,
    createdAt: 1,
    databaseName: "app_database",
    hostname: "database.shop.internal",
    id: "postgres/id",
    imageDigest: "sha256:postgres",
    imageTag: "17",
    name: "database",
    ownerPassword: "one-time-password",
    ownerUsername: "owner_database",
    port: 5432 as const,
    projectId: "project/id",
    updatedAt: 1,
  };
  await expect(
    createManagedPostgres(
      resource.projectId,
      { imageTag: "17", name: "database" },
      (input, init) => {
        expect(input.toString()).toBe("/api/v1/projects/project%2Fid/postgres");
        expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({
          imageTag: "17",
          name: "database",
        });
        return Promise.resolve(Response.json(resource, { status: 201 }));
      }
    )
  ).resolves.toEqual(resource);
  await expect(
    fetchManagedPostgres(
      resource.projectId,
      resource.id,
      undefined,
      (input) => {
        expect(input.toString()).toBe(
          "/api/v1/projects/project%2Fid/postgres/postgres%2Fid"
        );
        return Promise.resolve(Response.json(resource));
      }
    )
  ).resolves.toEqual(resource);

  const sql = "DELETE FROM sessions WHERE expired_at < now(); SELECT 1;";
  const result = await queryManagedPostgres(
    resource.projectId,
    resource.id,
    sql,
    (input, init) => {
      expect(input.toString()).toBe(
        "/api/v1/projects/project%2Fid/postgres/postgres%2Fid/query"
      );
      expect(init?.method).toBe("POST");
      expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({ sql });
      return Promise.resolve(
        Response.json({
          auditRecorded: true,
          statements: [
            {
              columns: [],
              commandTag: "DELETE 2",
              rows: [],
              truncated: false,
            },
            {
              columns: [{ name: "?column?", typeOid: 23 }],
              commandTag: "SELECT 1",
              rows: [[{ text: "1" }]],
              truncated: false,
            },
          ],
          truncated: false,
        })
      );
    }
  );
  expect(result.statements[0]?.commandTag).toBe("DELETE 2");
  expect(result.statements[1]?.rows[0]?.[0]?.text).toBe("1");
});

test("uses the Access-only object storage browser contract", async () => {
  const resource = {
    accessKey: "ps3_access",
    backupEnabled: false,
    backupRetentionCount: 7,
    bucketName: "shop-assets",
    corsOrigins: [],
    createdAt: 1,
    credentialPermission: "read_write" as const,
    id: "store/id",
    internalHostname: "assets.shop.internal",
    name: "assets",
    projectId: "project/id",
    region: "us-east-1" as const,
    secret: "shown-once",
    updatedAt: 1,
  };
  await expect(
    createObjectStore(
      resource.projectId,
      {
        bucketName: resource.bucketName,
        corsOrigins: ["https://app.example.com"],
        name: resource.name,
      },
      (input, init) => {
        expect(input.toString()).toBe(
          "/api/v1/projects/project%2Fid/object-stores"
        );
        expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({
          bucketName: "shop-assets",
          corsOrigins: ["https://app.example.com"],
          name: "assets",
        });
        return Promise.resolve(Response.json(resource, { status: 201 }));
      }
    )
  ).resolves.toEqual(resource);

  const persisted = { ...resource, accessKey: undefined, secret: undefined };
  await expect(
    fetchObjectStore(resource.projectId, resource.id, undefined, (input) => {
      expect(input.toString()).toBe(
        "/api/v1/projects/project%2Fid/object-stores/store%2Fid"
      );
      return Promise.resolve(Response.json(persisted));
    })
  ).resolves.toEqual(persisted);

  const metadata = {
    contentType: "text/plain",
    createdAt: 1,
    etag: '"digest"',
    objectKey: "docs/hello world.txt",
    size: 5,
    updatedAt: 2,
  };
  await expect(
    fetchObjects(
      resource.projectId,
      resource.id,
      { continuationToken: "cursor+/=", limit: 25, prefix: "docs/" },
      undefined,
      (input) => {
        expect(input.toString()).toContain("limit=25");
        expect(input.toString()).toContain("prefix=docs%2F");
        expect(input.toString()).toContain("continuationToken=cursor%2B%2F%3D");
        return Promise.resolve(
          Response.json({ nextContinuationToken: "", objects: [metadata] })
        );
      }
    )
  ).resolves.toMatchObject({ objects: [metadata] });

  await expect(
    previewObject(
      resource.projectId,
      resource.id,
      metadata.objectKey,
      undefined,
      (input) => {
        expect(input.toString()).toContain("key=docs%2Fhello+world.txt");
        return Promise.resolve(
          Response.json({ allowed: true, metadata, text: "hello" })
        );
      }
    )
  ).resolves.toMatchObject({ allowed: true, text: "hello" });

  const file = new Blob(["hello"], { type: "text/plain" });
  let uploadInit: RequestInit | undefined;
  await expect(
    uploadObject(
      resource.projectId,
      resource.id,
      metadata.objectKey,
      file,
      (_input, init) => {
        uploadInit = init;
        return Promise.resolve(Response.json(metadata, { status: 201 }));
      }
    )
  ).resolves.toEqual(metadata);
  expect(uploadInit?.method).toBe("PUT");
  expect(uploadInit?.body).toBe(file);
  expect(new Headers(uploadInit?.headers).get("Content-Type")).toContain(
    "text/plain"
  );

  await deleteObject(
    resource.projectId,
    resource.id,
    metadata.objectKey,
    (input, init) => {
      expect(input.toString()).toContain("key=docs%2Fhello+world.txt");
      expect(init?.method).toBe("DELETE");
      return Promise.resolve(new Response(null, { status: 204 }));
    }
  );
  expect(
    objectDownloadURL(resource.projectId, resource.id, metadata.objectKey)
  ).toContain("/objects/download?key=docs%2Fhello+world.txt");
});

test("lists, attaches, moves, and detaches exact service domains", async () => {
  const domain = {
    createdAt: 1,
    hostname: "api.example.com",
    projectId: "project",
    projectName: "shop",
    serviceId: "service",
    serviceName: "api",
  };
  await expect(
    fetchServiceDomains("project", "service", undefined, () =>
      Promise.resolve(Response.json({ domains: [domain] }))
    )
  ).resolves.toEqual([domain]);

  let attachBody = "";
  await expect(
    attachServiceDomain(
      "project",
      "service",
      domain.hostname,
      true,
      (_input, init) => {
        attachBody = init?.body?.toString() ?? "";
        return Promise.resolve(Response.json(domain, { status: 201 }));
      }
    )
  ).resolves.toEqual(domain);
  expect(JSON.parse(attachBody)).toEqual({
    hostname: domain.hostname,
    move: true,
  });

  let detachURL = "";
  let detachMethod = "";
  await detachServiceDomain(
    "project/one",
    "service/two",
    domain.hostname,
    (input, init) => {
      detachURL = input.toString();
      detachMethod = init?.method ?? "";
      return Promise.resolve(new Response(null, { status: 204 }));
    }
  );
  expect(detachURL).toBe(
    "/api/v1/projects/project%2Fone/services/service%2Ftwo/domains/api.example.com"
  );
  expect(detachMethod).toBe("DELETE");

  const conflict = await attachServiceDomain(
    "project",
    "service",
    domain.hostname,
    false,
    () =>
      Bun.sleep(0).then(() =>
        Response.json(
          {
            error: {
              code: "domain_conflict",
              domain,
              message: "Domain belongs to another service",
            },
          },
          { status: 409 }
        )
      )
  ).catch((error: unknown) => error);
  expect(conflict).toBeInstanceOf(APIError);
  expect((conflict as APIError).domain).toEqual(domain);
});

test("creates and revokes one-time API tokens", async () => {
  const token = {
    createdAt: 1,
    id: "token-id",
    name: "deploy-bot",
    projectId: "project",
    role: "admin" as const,
  };
  await expect(
    fetchAPITokens(undefined, () =>
      Promise.resolve(Response.json({ tokens: [token] }))
    )
  ).resolves.toEqual([token]);

  let createBody = "";
  await expect(
    createAPIToken(
      { name: token.name, projectId: token.projectId, role: token.role },
      (_input, init) => {
        createBody = init?.body?.toString() ?? "";
        return Promise.resolve(
          Response.json({ ...token, token: "ptk_token-id_secret" })
        );
      }
    )
  ).resolves.toMatchObject({ token: "ptk_token-id_secret" });
  expect(JSON.parse(createBody)).toEqual({
    name: token.name,
    projectId: token.projectId,
    role: token.role,
  });

  let revokeURL = "";
  await revokeAPIToken("token/with slash", (input, init) => {
    revokeURL = input.toString();
    expect(init?.method).toBe("DELETE");
    return Promise.resolve(new Response(null, { status: 204 }));
  });
  expect(revokeURL).toBe("/api/v1/tokens/token%2Fwith%20slash");
});

test("uses the Registry settings, repository, image, and deletion contracts", async () => {
  const repository = {
    backupEnabled: false,
    backupRetentionCount: 7,
    blobCount: 2,
    createdAt: 1,
    id: "repository/id",
    manifestCount: 1,
    name: "team/api",
    publicPull: false,
    referencedBlobBytes: 42,
    tagCount: 1,
    totalBlobBytes: 42,
    updatedAt: 1,
  };
  const image = {
    blobDigests: ["sha256:blob"],
    digest: "sha256:digest",
    manifestSize: 42,
    mediaType: "application/vnd.oci.image.manifest.v1+json",
    platforms: [],
    pushedAt: 1,
    referencedBlobBytes: 42,
    tags: ["latest"],
  };
  await expect(
    fetchRegistrySettings(undefined, () =>
      Promise.resolve(Response.json({ hostname: "registry.example.com" }))
    )
  ).resolves.toEqual({ hostname: "registry.example.com" });

  let hostnameBody = "";
  await setRegistryHostname("registry.example.com", (_input, init) => {
    hostnameBody = init?.body?.toString() ?? "";
    return Promise.resolve(Response.json({ hostname: "registry.example.com" }));
  });
  expect(JSON.parse(hostnameBody)).toEqual({
    hostname: "registry.example.com",
  });

  await expect(
    fetchRegistryRepositories(undefined, () =>
      Promise.resolve(Response.json({ repositories: [repository] }))
    )
  ).resolves.toEqual([repository]);
  await expect(
    createRegistryRepository(
      {
        credentialName: "deployer",
        credentialPermission: "pull_push",
        name: repository.name,
        publicPull: false,
      },
      () => Promise.resolve(Response.json(repository, { status: 201 }))
    )
  ).resolves.toEqual(repository);

  let publicPullBody = "";
  await expect(
    setRegistryRepositoryPublicPull(repository.id, true, (_input, init) => {
      publicPullBody = init?.body?.toString() ?? "";
      return Promise.resolve(
        Response.json({ ...repository, publicPull: true })
      );
    })
  ).resolves.toMatchObject({ publicPull: true });
  expect(JSON.parse(publicPullBody)).toEqual({ publicPull: true });

  await expect(
    fetchRegistryImages(repository.id, {}, undefined, (input) => {
      expect(input.toString()).toContain("repository%2Fid/images?limit=100");
      return Promise.resolve(
        Response.json({ images: [image], nextCursor: "" })
      );
    })
  ).resolves.toEqual({ images: [image], nextCursor: "" });
  await expect(
    fetchRegistryImage(repository.id, image.digest, undefined, (input) => {
      expect(input.toString()).toContain("sha256%3Adigest");
      return Promise.resolve(
        Response.json({ ...image, manifest: { schemaVersion: 2 } })
      );
    })
  ).resolves.toMatchObject({ manifest: { schemaVersion: 2 } });

  const deletionFetcher = ((input, init) => {
    expect(input.toString()).not.toContain("repository/id");
    expect(init?.method).toBe("DELETE");
    return Promise.resolve(new Response(null, { status: 204 }));
  }) as typeof fetch;
  await Promise.all([
    deleteRegistryTag(repository.id, "latest/tag", deletionFetcher),
    deleteRegistryImage(repository.id, image.digest, deletionFetcher),
    deleteRegistryRepository(repository.id, repository.name, deletionFetcher),
  ]);

  const credential = {
    createdAt: 1,
    id: "credential/id",
    name: "reader",
    permission: "pull" as const,
  };
  await expect(
    fetchRegistryCredentials(repository.id, undefined, () =>
      Promise.resolve(Response.json({ credentials: [credential] }))
    )
  ).resolves.toEqual([credential]);
  await expect(
    createRegistryCredential(
      repository.id,
      { name: credential.name, permission: credential.permission },
      () =>
        Promise.resolve(
          Response.json(
            { ...credential, secret: "secret", username: "robot" },
            { status: 201 }
          )
        )
    )
  ).resolves.toMatchObject({ secret: "secret", username: "robot" });
  await deleteRegistryCredential(repository.id, credential.id, deletionFetcher);
  await expect(
    cleanupRegistryRepository(repository.id, true, (_input, init) => {
      expect(JSON.parse(init?.body?.toString() ?? "")).toEqual({
        dryRun: true,
      });
      return Promise.resolve(
        Response.json({
          blobCount: 1,
          bytes: 42,
          deleted: false,
          previewDigests: ["sha256:orphan"],
          previewTruncated: false,
        })
      );
    })
  ).resolves.toMatchObject({ blobCount: 1, deleted: false });
});

test("configures the single probed backup target without returning its secret", async () => {
  const target = {
    accessKeyId: "remote-access",
    bucket: "backup-bucket",
    configured: true,
    createdAt: 1,
    endpoint: "https://s3.example.com",
    prefix: "platformd/test",
    region: "eu-central-003",
    updatedAt: 1,
  };
  await expect(
    fetchBackupTarget(undefined, () => Promise.resolve(Response.json(target)))
  ).resolves.toEqual(target);

  let requestBody = "";
  await expect(
    setBackupTarget(
      {
        accessKeyId: target.accessKeyId,
        bucket: target.bucket,
        endpoint: target.endpoint,
        prefix: target.prefix,
        region: target.region,
        secretAccessKey: "remote-secret",
      },
      (_input, init) => {
        requestBody = init?.body?.toString() ?? "";
        return Promise.resolve(Response.json(target));
      }
    )
  ).resolves.toEqual(target);
  expect(JSON.parse(requestBody)).toMatchObject({
    secretAccessKey: "remote-secret",
  });

  await deleteBackupTarget((_input, init) => {
    expect(init?.method).toBe("DELETE");
    return Promise.resolve(new Response(null, { status: 204 }));
  });
});

test("manages one exact resource backup policy and run history", async () => {
  const policy = {
    cron: "0 3 * * *",
    enabled: true,
    nextRunAt: 1_784_000_000_000,
    resourceId: "database/one",
    resourceKind: "postgres" as const,
    retentionCount: 7,
  };
  await expect(
    fetchBackupPolicies(undefined, (input) => {
      expect(input.toString()).toBe("/api/v1/backups/resources");
      return Promise.resolve(Response.json({ policies: [policy] }));
    })
  ).resolves.toEqual([policy]);
  await expect(
    fetchBackupPolicy(
      policy.resourceKind,
      policy.resourceId,
      undefined,
      (input) => {
        expect(input.toString()).toBe(
          "/api/v1/backups/resources/postgres/database%2Fone/policy"
        );
        return Promise.resolve(Response.json(policy));
      }
    )
  ).resolves.toEqual(policy);

  let policyBody = "";
  await expect(
    setBackupPolicy(
      policy.resourceKind,
      policy.resourceId,
      {
        cron: policy.cron,
        enabled: policy.enabled,
        retentionCount: policy.retentionCount,
      },
      (input, init) => {
        expect(input.toString()).toBe(
          "/api/v1/backups/resources/postgres/database%2Fone/policy"
        );
        policyBody = init?.body?.toString() ?? "";
        return Promise.resolve(Response.json(policy));
      }
    )
  ).resolves.toEqual(policy);
  expect(JSON.parse(policyBody)).toEqual({
    cron: policy.cron,
    enabled: true,
    retentionCount: 7,
  });

  const record = {
    id: "backup-1",
    resourceId: policy.resourceId,
    resourceKind: policy.resourceKind,
    startedAt: 43,
    status: "running" as const,
  };
  await expect(
    runBackupNow(policy.resourceKind, policy.resourceId, (input, init) => {
      expect(input.toString()).toBe(
        "/api/v1/backups/resources/postgres/database%2Fone/run"
      );
      expect(init?.method).toBe("POST");
      return Promise.resolve(Response.json(record, { status: 202 }));
    })
  ).resolves.toEqual(record);
  await expect(
    fetchBackupHistory(
      policy.resourceKind,
      policy.resourceId,
      undefined,
      (input) => {
        expect(input.toString()).toBe(
          "/api/v1/backups/resources/postgres/database%2Fone/history?limit=50"
        );
        return Promise.resolve(Response.json({ backups: [record] }));
      }
    )
  ).resolves.toEqual([record]);
});

test("lists recovery generations and starts an explicitly destructive replacement", async () => {
  const generation = {
    completedAt: 42,
    generationId: "generation-1",
    plaintextSize: 100,
    remoteSize: 120,
  };
  await expect(
    fetchBackupGenerations("postgres", "database/one", undefined, (input) => {
      expect(input.toString()).toBe(
        "/api/v1/backups/resources/postgres/database%2Fone/generations"
      );
      return Promise.resolve(Response.json({ generations: [generation] }));
    })
  ).resolves.toEqual([generation]);

  const operation = {
    id: "operation-1",
    kind: "postgres_restore",
    progress: "starting",
    startedAt: 43,
    status: "running" as const,
    targetId: "database/one",
  };
  let restoreBody = "";
  await expect(
    restoreBackupGeneration(
      "postgres",
      "database/one",
      generation.generationId,
      (input, init) => {
        expect(input.toString()).toBe(
          "/api/v1/backups/resources/postgres/database%2Fone/restore"
        );
        restoreBody = init?.body?.toString() ?? "";
        return Promise.resolve(Response.json(operation, { status: 202 }));
      }
    )
  ).resolves.toEqual(operation);
  expect(JSON.parse(restoreBody)).toEqual({
    destructiveConfirmed: true,
    generationId: generation.generationId,
    mode: "replace",
  });
});

test("polls recovery and observational operation status then requests a retry", async () => {
  const resource = {
    generationId: "generation-1",
    resourceId: "redis-1",
    resourceKind: "redis" as const,
    sourceCompletedAt: 42,
    status: "restored" as const,
  };
  await expect(
    fetchRecoveryStatus(undefined, (input) => {
      expect(input.toString()).toBe("/api/v1/recovery");
      return Promise.resolve(
        Response.json({ lastError: "postgres failed", resources: [resource] })
      );
    })
  ).resolves.toEqual({
    lastError: "postgres failed",
    resources: [resource],
  });

  await expect(
    fetchOperation("operation/one", undefined, (input) => {
      expect(input.toString()).toBe("/api/v1/operations/operation%2Fone");
      return Promise.resolve(
        Response.json({
          finishedAt: 44,
          id: "operation/one",
          kind: "redis_restore",
          startedAt: 43,
          status: "succeeded",
          targetId: "redis-1",
        })
      );
    })
  ).resolves.toMatchObject({ status: "succeeded" });

  await retryRecovery((_input, init) => {
    expect(init?.method).toBe("POST");
    return Promise.resolve(new Response(null, { status: 202 }));
  });
});
