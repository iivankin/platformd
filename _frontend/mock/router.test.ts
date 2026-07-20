import { describe, expect, test } from "bun:test";

import {
  createAPIToken,
  createBackupTarget,
  configureGitHubApp,
  createProject,
  createRegistryRepository,
  deleteService,
  fetchAPITokens,
  fetchBackupGenerations,
  fetchBackupHistory,
  fetchAuditEvents,
  fetchBackupPolicies,
  fetchBackupPolicy,
  fetchBackupTargets,
  fetchContainerFiles,
  fetchContainerPorts,
  fetchDiskPressure,
  fetchIdentity,
  fetchGitHubAppSettings,
  fetchGitHubRepositories,
  fetchInfrastructureLogs,
  fetchInstallationSettings,
  fetchManagedPostgres,
  fetchManagedPostgresExtensions,
  fetchManagedRedis,
  fetchMeta,
  fetchObjects,
  fetchObjectStore,
  fetchProjectCanvas,
  fetchProjects,
  fetchRegistryImage,
  fetchRegistryImages,
  fetchRegistryRepositories,
  fetchRegistrySettings,
  fetchResourceLogs,
  fetchResourceUsage,
  fetchResourceUsageHistory,
  fetchResourceTerminalShells,
  fetchResolvedServiceEnvironment,
  fetchService,
  fetchServiceDeployment,
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchServiceListeners,
  fetchVolumes,
  scanManagedRedisKeys,
  setAutomationHostname,
  setRegistryHostname,
  setManagedPostgresExtension,
  uploadContainerFile,
} from "../web/api";
import { handleMockAPI } from "./router";
import { createMockState } from "./state";
import type { MockState } from "./state";

type MockFetcher = (
  input: RequestInfo | URL,
  init?: RequestInit
) => Promise<Response>;

const fetcher =
  (state: MockState): MockFetcher =>
  (input, init) => {
    const value = input instanceof Request ? input.url : input.toString();
    const url = new URL(value, "http://platformd.mock");
    return handleMockAPI(new Request(url, init), state);
  };

describe("mock API", () => {
  test("returns detected listening ports for live resources", async () => {
    const mockFetch = fetcher(createMockState("demo"));

    await expect(
      fetchContainerPorts(
        "project-demo",
        "service",
        "service-api",
        undefined,
        mockFetch
      )
    ).resolves.toEqual([
      { port: 3000, protocol: "tcp" },
      { port: 5353, protocol: "udp" },
      { port: 8080, protocol: "tcp" },
    ]);
  });

  test("deletes a service and its canvas-owned state", async () => {
    const state = createMockState("demo");
    const mockFetch = fetcher(state);
    const service = await fetchService(
      "project-demo",
      "service-api",
      undefined,
      mockFetch
    );
    await deleteService(
      "project-demo",
      service.id,
      service.updatedAt,
      mockFetch
    );
    await expect(
      fetchService("project-demo", service.id, undefined, mockFetch)
    ).rejects.toMatchObject({ code: "not_found" });
    const canvas = await fetchProjectCanvas(
      "project-demo",
      undefined,
      mockFetch
    );
    expect(
      canvas.resources.some((resource) => resource.id === service.id)
    ).toBe(false);
    expect(canvas.project.serviceCount).toBe(0);
  });

  test("demo fixtures satisfy the frontend response contracts", async () => {
    const state = createMockState("demo");
    const mockFetch = fetcher(state);

    const [
      meta,
      identity,
      githubSettings,
      githubRepositories,
      projects,
      backupTargets,
      backupPolicies,
      pressure,
      infrastructureLogs,
      audit,
      registry,
      repositories,
      tokens,
      settings,
    ] = await Promise.all([
      fetchMeta(undefined, mockFetch),
      fetchIdentity(undefined, mockFetch),
      fetchGitHubAppSettings(undefined, mockFetch),
      fetchGitHubRepositories(undefined, mockFetch),
      fetchProjects(undefined, mockFetch),
      fetchBackupTargets(undefined, mockFetch),
      fetchBackupPolicies(undefined, mockFetch),
      fetchDiskPressure(undefined, mockFetch),
      fetchInfrastructureLogs(500, undefined, mockFetch),
      fetchAuditEvents({}, undefined, mockFetch),
      fetchRegistrySettings(undefined, mockFetch),
      fetchRegistryRepositories(undefined, mockFetch),
      fetchAPITokens(undefined, mockFetch),
      fetchInstallationSettings(undefined, mockFetch),
    ]);

    expect(meta.status).toBe("ready");
    expect(identity.email).toBe("developer@mock.local");
    expect(githubSettings.configured).toBe(true);
    expect(githubRepositories).toHaveLength(1);
    expect(projects).toHaveLength(1);
    const [firstProject] = projects;
    expect(firstProject).toBeDefined();
    const canvas = await fetchProjectCanvas(
      firstProject?.id ?? "missing-project",
      undefined,
      mockFetch
    );
    expect(canvas).toBeDefined();
    expect(backupTargets.targets).toHaveLength(1);
    expect(backupPolicies).toHaveLength(4);
    expect(pressure.level).toBe("normal");
    expect(infrastructureLogs.records).not.toHaveLength(0);
    expect(audit.events).not.toHaveLength(0);
    expect(registry.hostname).toBe("registry.mock.local");
    expect(repositories).toHaveLength(1);
    expect(tokens).toHaveLength(2);
    expect(settings.certificates).toHaveLength(1);
  });

  test("demo fixtures support the resource detail screens", async () => {
    const mockFetch = fetcher(createMockState("demo"));
    const [
      service,
      deployments,
      domains,
      listeners,
      volumes,
      redis,
      redisKeys,
      postgres,
      objectStore,
      objects,
      backupPolicy,
      backupHistory,
      backupGenerations,
      registryImages,
      redisLogs,
      postgresLogs,
      objectStoreLogs,
      serviceUsage,
      serviceUsageHistory,
      resolvedEnvironment,
    ] = await Promise.all([
      fetchService("project-demo", "service-api", undefined, mockFetch),
      fetchServiceDeployments(
        "project-demo",
        "service-api",
        undefined,
        undefined,
        mockFetch
      ),
      fetchServiceDomains("project-demo", "service-api", undefined, mockFetch),
      fetchServiceListeners(
        "project-demo",
        "service-api",
        undefined,
        mockFetch
      ),
      fetchVolumes("project-demo", "service-api", undefined, mockFetch),
      fetchManagedRedis("project-demo", "redis-cache", undefined, mockFetch),
      scanManagedRedisKeys(
        "project-demo",
        "redis-cache",
        {},
        undefined,
        mockFetch
      ),
      fetchManagedPostgres(
        "project-demo",
        "postgres-main",
        undefined,
        mockFetch
      ),
      fetchObjectStore("project-demo", "object-assets", undefined, mockFetch),
      fetchObjects("project-demo", "object-assets", {}, undefined, mockFetch),
      fetchBackupPolicy("postgres", "postgres-main", undefined, mockFetch),
      fetchBackupHistory(
        "postgres",
        "postgres-main",
        "backup-target-primary",
        undefined,
        mockFetch
      ),
      fetchBackupGenerations(
        "postgres",
        "postgres-main",
        "backup-target-primary",
        undefined,
        mockFetch
      ),
      fetchRegistryImages("repository-api", {}, undefined, mockFetch),
      fetchResourceLogs(
        "project-demo",
        "redis",
        "redis-cache",
        {},
        undefined,
        mockFetch
      ),
      fetchResourceLogs(
        "project-demo",
        "postgres",
        "postgres-main",
        {},
        undefined,
        mockFetch
      ),
      fetchResourceLogs(
        "project-demo",
        "object_store",
        "object-assets",
        {},
        undefined,
        mockFetch
      ),
      fetchResourceUsage("service", "service-api", undefined, mockFetch),
      fetchResourceUsageHistory(
        "service",
        "service-api",
        "1h",
        undefined,
        mockFetch
      ),
      fetchResolvedServiceEnvironment(
        "project-demo",
        "service-api",
        undefined,
        mockFetch
      ),
    ]);

    expect(service.name).toBe("api");
    expect(deployments.deployments).toHaveLength(3);
    expect(domains).toHaveLength(1);
    expect(listeners).toHaveLength(1);
    expect(volumes).toEqual([]);
    expect(redis.name).toBe("cache");
    expect(redisKeys.keys).toHaveLength(1);
    expect(postgres.databaseName).toBe("app");
    expect(objectStore.bucketName).toBe("storefront-assets");
    expect(objects.objects).not.toHaveLength(0);
    expect(backupPolicy.enabled).toBe(true);
    expect(backupHistory).not.toHaveLength(0);
    expect(backupGenerations).not.toHaveLength(0);
    expect(registryImages.images).toHaveLength(1);
    expect(redisLogs.records).not.toHaveLength(0);
    expect(postgresLogs.records).not.toHaveLength(0);
    expect(objectStoreLogs.records).not.toHaveLength(0);
    expect(serviceUsage.running).toBe(true);
    expect(serviceUsage.networkAvailable).toBe(true);
    expect(serviceUsageHistory.points).not.toHaveLength(0);
    expect(resolvedEnvironment.POSTGRES_URL).toContain(
      "postgresql://app_owner:mock-only-postgres-password@postgres-main.storefront.internal:5432/app"
    );
    expect(resolvedEnvironment.REDIS_URL).not.toContain("${{");

    const selectedDeployment = await fetchServiceDeployment(
      "project-demo",
      "service-api",
      "deployment-failed",
      undefined,
      mockFetch
    );
    const selectedDeploymentLogs = await fetchResourceLogs(
      "project-demo",
      "service",
      "service-api",
      { deploymentId: selectedDeployment.id },
      undefined,
      mockFetch
    );
    expect(selectedDeployment.status).toBe("failed");
    expect(selectedDeploymentLogs.records).toHaveLength(1);
    expect(selectedDeploymentLogs.records[0]?.deploymentId).toBe(
      selectedDeployment.id
    );

    const image = await fetchRegistryImage(
      "repository-api",
      registryImages.images[0]?.digest ?? "missing-digest",
      undefined,
      mockFetch
    );
    expect(image.tags).toContain("stable");
  });

  test("mock PostgreSQL extensions can be installed and uninstalled", async () => {
    const mockFetch = fetcher(createMockState("demo"));
    const initial = await fetchManagedPostgresExtensions(
      "project-demo",
      "postgres-main",
      undefined,
      mockFetch
    );
    expect(
      initial.find((extension) => extension.name === "uuid-ossp")
    ).not.toHaveProperty("installedVersion");

    await setManagedPostgresExtension(
      "project-demo",
      "postgres-main",
      "uuid-ossp",
      true,
      mockFetch
    );
    const installed = await fetchManagedPostgresExtensions(
      "project-demo",
      "postgres-main",
      undefined,
      mockFetch
    );
    expect(
      installed.find((extension) => extension.name === "uuid-ossp")
        ?.installedVersion
    ).toBe("1.1");

    await setManagedPostgresExtension(
      "project-demo",
      "postgres-main",
      "uuid-ossp",
      false,
      mockFetch
    );
    const removed = await fetchManagedPostgresExtensions(
      "project-demo",
      "postgres-main",
      undefined,
      mockFetch
    );
    expect(
      removed.find((extension) => extension.name === "uuid-ossp")
    ).not.toHaveProperty("installedVersion");
  });

  test("mutations update only the in-memory state", async () => {
    const state = createMockState("empty");
    const mockFetch = fetcher(state);

    const project = await createProject("preview", mockFetch);
    const token = await createAPIToken(
      { name: "preview-token", projectId: project.id, role: "admin" },
      mockFetch
    );
    await createBackupTarget(
      {
        accessKeyId: "MOCK_ACCESS_KEY",
        bucket: "mock-backups",
        endpoint: "https://s3.mock.local",
        name: "Preview storage",
        prefix: "preview",
        region: "mock-region-1",
        secretAccessKey: "mock-only-secret",
      },
      mockFetch
    );
    await setRegistryHostname("registry.preview.local", mockFetch);
    const repository = await createRegistryRepository(
      {
        credentialName: "deployer",
        credentialPermission: "pull_push",
        name: "preview/api",
        publicPull: false,
      },
      mockFetch
    );
    await setAutomationHostname("api.preview.local", mockFetch);
    const githubSettings = await configureGitHubApp(
      {
        appId: 42,
        privateKeyPem: "mock-private-key",
        webhookSecret: "mock-webhook-secret",
      },
      mockFetch
    );

    const projects = await fetchProjects(undefined, mockFetch);
    const backupTargets = await fetchBackupTargets(undefined, mockFetch);
    const registrySettings = await fetchRegistrySettings(undefined, mockFetch);
    const repositories = await fetchRegistryRepositories(undefined, mockFetch);
    const settings = await fetchInstallationSettings(undefined, mockFetch);
    expect(projects).toEqual([project]);
    expect(token.token).toBe("mock-only-token-do-not-use");
    expect(backupTargets.targets[0]?.bucket).toBe("mock-backups");
    expect(registrySettings.hostname).toBe("registry.preview.local");
    expect(repositories[0]?.id).toBe(repository.id);
    expect(settings.automationHostname).toBe("api.preview.local");
    expect(githubSettings).toMatchObject({ appId: 42, configured: true });
  });

  test("mock container resources expose shells and mutable file trees", async () => {
    const state = createMockState("demo");
    const mockFetch = fetcher(state);

    await expect(
      fetchResourceTerminalShells(
        "project-demo",
        "service",
        "service-api",
        undefined,
        mockFetch
      )
    ).resolves.toEqual(["/bin/sh", "/bin/bash"]);

    const initial = await fetchContainerFiles(
      "project-demo",
      "service",
      "service-api",
      "/app",
      undefined,
      mockFetch
    );
    expect(
      initial.entries.some((entry) => entry.path === "/app/README.md")
    ).toBe(true);

    await uploadContainerFile(
      "project-demo",
      "service",
      "service-api",
      "/app/upload.txt",
      new File(["uploaded"], "upload.txt"),
      mockFetch
    );
    const updated = await fetchContainerFiles(
      "project-demo",
      "service",
      "service-api",
      "/app",
      undefined,
      mockFetch
    );
    expect(
      updated.entries.some((entry) => entry.path === "/app/upload.txt")
    ).toBe(true);
  });

  test("error scenario produces deliberate API failures", async () => {
    const mockFetch = fetcher(createMockState("error"));
    await expect(fetchMeta(undefined, mockFetch)).rejects.toThrow(
      "meta request failed with 503"
    );
  });
});
