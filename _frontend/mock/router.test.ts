import { describe, expect, test } from "bun:test";

import {
  createAPIToken,
  createBackupTarget,
  createErrorTracker,
  configureCloudflareMesh,
  createNetworkGateway,
  createProject,
  deleteNetworkGateway,
  deleteProject,
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
  fetchCloudflareMeshCredential,
  fetchCloudflareMeshSettings,
  fetchDiskPressure,
  fetchErrorTracker,
  fetchIdentity,
  fetchInfrastructureLogs,
  fetchInstallationSettings,
  fetchManagedPostgres,
  fetchManagedPostgresExtensions,
  fetchManagedPostgresStats,
  fetchManagedPostgresStatsHistory,
  fetchManagedRedis,
  fetchManagedRedisStats,
  fetchManagedRedisStatsHistory,
  fetchHostNetworkAddresses,
  fetchMeta,
  fetchNetworkGateway,
  fetchObjects,
  fetchObjectStore,
  fetchProjectCanvas,
  fetchProjects,
  fetchResourceLogs,
  fetchResourceUsage,
  fetchResourceUsageHistory,
  fetchSelfUpdateStatus,
  fetchResourceTerminalShells,
  fetchResolvedServiceEnvironment,
  fetchService,
  fetchServiceDeployment,
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchServiceListeners,
  fetchVolumes,
  scanManagedRedisKeys,
  setAdminHostname,
  setCloudflareAccessConfiguration,
  setManagedPostgresExtension,
  uploadContainerFile,
  updateErrorTrackerPublicAccess,
  queryManagedPostgres,
} from "../web/api";
import {
  postgresDeleteRowsSQL,
  postgresEnumCatalogSQL,
  postgresEnumsFromResult,
  postgresForeignKeyCatalogSQL,
  postgresForeignKeysFromResult,
  postgresOutgoingRelationForColumn,
  postgresRelationsForTable,
  postgresTableDataSQL,
  postgresUpdateCellSQL,
} from "../web/postgres-data-browser-model";
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
  test("creates an imported network gateway and removes it from the canvas", async () => {
    const state = createMockState("demo");
    const mockFetch = fetcher(state);

    await expect(
      fetchHostNetworkAddresses(undefined, mockFetch)
    ).resolves.toContainEqual({
      address: "100.64.0.10",
      interface: "tailscale0",
    });
    const gateway = await createNetworkGateway(
      "project-demo",
      {
        interfaceName: "tailscale0",
        listenPort: 5432,
        mode: "import",
        name: "warehouse-db",
        protocol: "tcp",
        remoteHost: "100.64.0.20",
        remotePort: 5432,
        sourceAddress: "100.64.0.10",
        targetPort: 0,
        targetServiceId: "",
        transport: "mesh",
      },
      mockFetch
    );

    await expect(
      fetchNetworkGateway("project-demo", gateway.id, undefined, mockFetch)
    ).resolves.toMatchObject({
      interfaceName: "",
      internalHostname: "warehouse-db.storefront.internal",
      mode: "import",
      sourceAddress: "",
    });
    let canvas = await fetchProjectCanvas("project-demo", undefined, mockFetch);
    expect(canvas.project.networkGatewayCount).toBe(1);
    expect(
      canvas.resources.some((resource) => resource.id === gateway.id)
    ).toBe(true);

    await deleteNetworkGateway("project-demo", gateway.id, mockFetch);
    canvas = await fetchProjectCanvas("project-demo", undefined, mockFetch);
    expect(canvas.project.networkGatewayCount).toBe(0);
    expect(
      canvas.resources.some((resource) => resource.id === gateway.id)
    ).toBe(false);
  });

  test("configures and reveals the installation-managed Mesh credential", async () => {
    const state = createMockState("empty");
    const mockFetch = fetcher(state);
    const accountId = "0123456789abcdef0123456789abcdef";
    const apiToken = "mock-cloudflare-mesh-api-token-value";

    await expect(
      fetchCloudflareMeshSettings(undefined, mockFetch)
    ).resolves.toMatchObject({ configured: false, status: "not_configured" });
    await expect(
      configureCloudflareMesh({ accountId, apiToken }, mockFetch)
    ).resolves.toMatchObject({
      configured: true,
      meshIp: "100.96.0.21",
      status: "connected",
    });
    await expect(
      fetchCloudflareMeshCredential(undefined, mockFetch)
    ).resolves.toEqual({ accountId, apiToken });
  });

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
      projects,
      backupTargets,
      backupPolicies,
      pressure,
      infrastructureLogs,
      audit,
      tokens,
      settings,
      updateStatus,
    ] = await Promise.all([
      fetchMeta(undefined, mockFetch),
      fetchIdentity(undefined, mockFetch),
      fetchProjects(undefined, mockFetch),
      fetchBackupTargets(undefined, mockFetch),
      fetchBackupPolicies(undefined, mockFetch),
      fetchDiskPressure(undefined, mockFetch),
      fetchInfrastructureLogs({ limit: 500 }, undefined, mockFetch),
      fetchAuditEvents({}, undefined, mockFetch),
      fetchAPITokens(undefined, mockFetch),
      fetchInstallationSettings(undefined, mockFetch),
      fetchSelfUpdateStatus(undefined, mockFetch),
    ]);

    expect(meta.status).toBe("ready");
    expect(identity.email).toBe("developer@mock.local");
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
    expect(backupPolicies).toHaveLength(3);
    expect(pressure.level).toBe("normal");
    expect(infrastructureLogs.records).not.toHaveLength(0);
    expect(audit.events).not.toHaveLength(0);
    expect(tokens).toHaveLength(2);
    expect(settings.certificates).toHaveLength(1);
    expect(updateStatus.updateAvailable).toBe(true);
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
  });

  test("mock managed stats snapshots and history are available", async () => {
    const mockFetch = fetcher(createMockState("demo"));
    await expect(
      fetchManagedPostgresStats(
        "project-demo",
        "postgres-main",
        undefined,
        mockFetch
      )
    ).resolves.toMatchObject({
      cacheHitPercent: 97.5,
      queriesPerSecond: 58.2,
      version: "PostgreSQL 17.5",
    });
    const postgresHistory = await fetchManagedPostgresStatsHistory(
      "project-demo",
      "postgres-main",
      "1h",
      undefined,
      mockFetch
    );
    expect(postgresHistory.points.length).toBeGreaterThan(0);
    expect(postgresHistory.points[0]?.metrics).toMatchObject({
      queriesPerSecond: expect.any(Number),
      transactionsPerSecond: expect.any(Number),
    });

    await expect(
      fetchManagedRedisStats(
        "project-demo",
        "redis-cache",
        undefined,
        mockFetch
      )
    ).resolves.toMatchObject({
      operationsPerSecond: 539,
      version: "8.2.1",
    });
    const redisHistory = await fetchManagedRedisStatsHistory(
      "project-demo",
      "redis-cache",
      "1h",
      undefined,
      mockFetch
    );
    expect(redisHistory.points.length).toBeGreaterThan(0);
    expect(redisHistory.points[0]?.metrics).toMatchObject({
      "cmd.get": expect.any(Number),
      operationsPerSecond: expect.any(Number),
      otherCommandsPerSecond: expect.any(Number),
    });
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

  test("mock PostgreSQL data browser exposes foreign-key relations", async () => {
    const mockFetch = fetcher(createMockState("demo"));
    const result = await queryManagedPostgres(
      "project-demo",
      "postgres-main",
      postgresForeignKeyCatalogSQL,
      undefined,
      mockFetch
    );
    const foreignKeys = postgresForeignKeysFromResult(result);
    expect(foreignKeys).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          columns: ["customer_id"],
          foreignTable: "customers",
          table: "orders",
        }),
        expect.objectContaining({
          columns: ["order_id"],
          foreignTable: "orders",
          table: "order_items",
        }),
        expect.objectContaining({
          columns: ["product_id"],
          foreignTable: "products",
          table: "order_items",
        }),
      ])
    );

    const orderRelations = postgresRelationsForTable(foreignKeys, {
      name: "orders",
      primaryKeyColumns: ["id"],
      schema: "public",
    });
    expect(
      postgresOutgoingRelationForColumn(orderRelations, "customer_id")?.label
    ).toBe("customers");
    expect(
      orderRelations.some((relation) => relation.direction === "incoming")
    ).toBe(true);
  });

  test("mock PostgreSQL data browser updates and deletes rows", async () => {
    const { resetMockPostgresData } = await import("./postgres-query");
    resetMockPostgresData();
    const mockFetch = fetcher(createMockState("demo"));
    const enums = postgresEnumsFromResult(
      await queryManagedPostgres(
        "project-demo",
        "postgres-main",
        postgresEnumCatalogSQL,
        undefined,
        mockFetch
      )
    );
    expect(enums.get(90_001)?.labels).toEqual([
      "paid",
      "fulfilled",
      "refunded",
      "pending",
    ]);

    const update = await queryManagedPostgres(
      "project-demo",
      "postgres-main",
      postgresUpdateCellSQL({
        column: "status",
        kind: "enum",
        rowValues: { id: "f9a1b942-7b35-4ac5-8168-b673bf627610" },
        table: {
          name: "orders",
          primaryKeyColumns: ["id"],
          schema: "public",
        },
        value: { kind: "text", text: "pending" },
      }),
      undefined,
      mockFetch
    );
    expect(update.statements[0]?.commandTag).toBe("UPDATE 1");

    const rows = await queryManagedPostgres(
      "project-demo",
      "postgres-main",
      postgresTableDataSQL({
        filters: [
          {
            column: "id",
            connector: "and",
            id: "id",
            operator: "=",
            value: "f9a1b942-7b35-4ac5-8168-b673bf627610",
          },
        ],
        page: 0,
        table: {
          name: "orders",
          primaryKeyColumns: ["id"],
          schema: "public",
        },
      }),
      undefined,
      mockFetch
    );
    expect(rows.statements[0]?.rows[0]?.[2]?.text).toBe("pending");

    const nulled = await queryManagedPostgres(
      "project-demo",
      "postgres-main",
      postgresUpdateCellSQL({
        column: "status",
        kind: "enum",
        rowValues: { id: "f9a1b942-7b35-4ac5-8168-b673bf627610" },
        table: {
          name: "orders",
          primaryKeyColumns: ["id"],
          schema: "public",
        },
        value: { kind: "null" },
      }),
      undefined,
      mockFetch
    );
    expect(nulled.statements[0]?.commandTag).toBe("UPDATE 1");

    const deleted = await queryManagedPostgres(
      "project-demo",
      "postgres-main",
      postgresDeleteRowsSQL({
        rows: [{ id: "a46ca6d8-5a91-45f0-8ae6-b6788e139f61" }],
        table: {
          name: "orders",
          primaryKeyColumns: ["id"],
          schema: "public",
        },
      }),
      undefined,
      mockFetch
    );
    expect(deleted.statements[0]?.commandTag).toBe("DELETE 1");
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
    await setAdminHostname("admin.preview.local", mockFetch);
    await setCloudflareAccessConfiguration(
      {
        audience: "preview-audience",
        teamDomain: "preview.cloudflareaccess.com",
      },
      mockFetch
    );
    const projects = await fetchProjects(undefined, mockFetch);
    const backupTargets = await fetchBackupTargets(undefined, mockFetch);
    const settings = await fetchInstallationSettings(undefined, mockFetch);
    expect(projects).toEqual([project]);
    expect(token.token).toBe("mock-only-token-do-not-use");
    expect(backupTargets.targets[0]?.bucket).toBe("mock-backups");
    expect(settings.adminHostname).toBe("admin.preview.local");
    expect(settings.accessAudience).toBe("preview-audience");
    expect(settings.accessTeamDomain).toBe("preview.cloudflareaccess.com");
  });

  test("mock error tracker embeds its console behind the resource route", async () => {
    const state = createMockState("demo");
    const mockFetch = fetcher(state);
    const tracker = await createErrorTracker(
      "project-demo",
      { name: "errors" },
      mockFetch
    );

    await expect(
      fetchErrorTracker("project-demo", tracker.id, undefined, mockFetch)
    ).resolves.toEqual(tracker);
    const canvas = await fetchProjectCanvas(
      "project-demo",
      undefined,
      mockFetch
    );
    expect(canvas.project.errorTrackerCount).toBe(1);
    expect(canvas.resources).toContainEqual(
      expect.objectContaining({ id: tracker.id, kind: "error_tracker" })
    );

    const updated = await updateErrorTrackerPublicAccess(
      "project-demo",
      tracker.id,
      {
        expectedUpdatedAt: tracker.updatedAt,
        publicHostname: "errors.mock.local",
      },
      mockFetch
    );
    const consoleResponse = await mockFetch(
      `/api/v1/projects/project-demo/error-trackers/${tracker.id}/console/api/v1/tracker`
    );
    expect(await consoleResponse.json()).toMatchObject({
      name: "errors",
      publicUrl: "https://errors.mock.local",
    });
    expect(updated.publicHostname).toBe("errors.mock.local");
  });

  test("deletes a project and all of its mock-owned resources", async () => {
    const state = createMockState("demo");
    const mockFetch = fetcher(state);

    await deleteProject(
      "project-demo",
      { deleteBackups: true, expectedName: "storefront" },
      mockFetch
    );

    await expect(fetchProjects(undefined, mockFetch)).resolves.toEqual([]);
    await expect(
      fetchProjectCanvas("project-demo", undefined, mockFetch)
    ).rejects.toMatchObject({ code: "not_found" });
    expect(Object.keys(state.services)).toEqual([]);
    expect(Object.keys(state.postgres)).toEqual([]);
    expect(Object.keys(state.redis)).toEqual([]);
    expect(Object.keys(state.objectStores)).toEqual([]);
    expect(Object.keys(state.errorTrackers)).toEqual([]);
    expect(state.backupPolicies).toHaveLength(0);
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
    expect(
      initial.entries.some(
        (entry) => entry.directory && entry.path === "/app/config"
      )
    ).toBe(true);
    expect(
      initial.entries.some((entry) => entry.path === "/app/config/runtime.json")
    ).toBe(false);

    const nested = await fetchContainerFiles(
      "project-demo",
      "service",
      "service-api",
      "/app/config",
      undefined,
      mockFetch
    );
    expect(nested.entries.map((entry) => entry.path)).toEqual([
      "/app/config/runtime.json",
    ]);

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
