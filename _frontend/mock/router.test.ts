import { describe, expect, test } from "bun:test";

import {
  attachServiceListener,
  createServiceMetricChart,
  createMetricChart,
  createAPIToken,
  createBackupTarget,
  createMailErrorAlert,
  createMailMetricAlert,
  configureCloudflareMesh,
  createNetworkGateway,
  createProject,
  deleteNetworkGateway,
  deleteMailErrorAlert,
  deleteMailMetricAlert,
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
  fetchIdentity,
  fetchInfrastructureLogs,
  fetchInstallationSettings,
  fetchMetricCatalog,
  fetchMetricCharts,
  fetchMetricQuery,
  fetchManagedPostgres,
  fetchManagedPostgresExtensions,
  fetchManagedPostgresStats,
  fetchManagedPostgresStatsHistory,
  fetchManagedRedis,
  fetchManagedRedisStats,
  fetchManagedRedisStatsHistory,
  fetchMailSettings,
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
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchServiceListeners,
  fetchServiceTelemetry,
  fetchScopedIssues,
  fetchServiceMetricCatalog,
  fetchServiceMetricCharts,
  fetchServiceMetricQuery,
  fetchServiceReplayRecording,
  fetchServiceTrace,
  fetchServiceTraces,
  fetchTelemetryLogs,
  fetchTelemetryTraces,
  fetchVolumes,
  scanManagedRedisKeys,
  setAdminHostname,
  setCloudflareAccessConfiguration,
  setManagedPostgresExtension,
  uploadContainerFile,
  updateServiceMetricChart,
  updateServiceTelemetryBrowserTunnel,
  updateServiceTelemetryPublicAccess,
  queryManagedPostgres,
  saveSMTPSettings,
  sendTestMail,
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

  test("configures installation mail sending without returning the SMTP password", async () => {
    const mockFetch = fetcher(createMockState("demo"));
    const settings = await fetchMailSettings(undefined, mockFetch);
    expect(settings.smtp).toMatchObject({
      configured: true,
      host: "smtp.mock.local",
      passwordSet: true,
    });
    expect(settings.smtp).not.toHaveProperty("password");
    expect(settings.errorAlerts).toHaveLength(1);
    expect(settings.metricAlerts[0]?.serviceId).toBe("service-api");
    expect(settings.services).toEqual([
      {
        id: "service-api",
        name: "api",
        projectId: "project-demo",
        projectName: "storefront",
      },
      {
        id: "service-worker",
        name: "worker",
        projectId: "project-demo",
        projectName: "storefront",
      },
    ]);

    const smtp = await saveSMTPSettings(
      {
        encryption: "tls",
        fromAddress: "alerts@mock.local",
        fromName: "platformd",
        host: "localhost",
        password: "replacement-secret",
        port: 465,
        username: "alerts",
      },
      mockFetch
    );
    expect(smtp).toMatchObject({
      configured: true,
      encryption: "tls",
      host: "localhost",
      passwordSet: true,
      port: 465,
    });
    expect(smtp).not.toHaveProperty("password");
    await sendTestMail("ops@mock.local", mockFetch);

    const errorAlert = await createMailErrorAlert(
      {
        enabled: true,
        eventTypes: ["issue_resolved"],
        name: "Resolved issues",
        recipients: ["oncall@mock.local"],
        serviceIds: [],
      },
      mockFetch
    );
    expect(errorAlert.serviceIds).toEqual([]);
    const metricAlert = await createMailMetricAlert(
      {
        enabled: true,
        name: "Error rate",
        operator: "gte",
        recipients: ["oncall@mock.local"],
        scope: "installation",
        sql: "SELECT bucket AS time, sum(value) AS value FROM metrics GROUP BY bucket",
        threshold: 10,
        windowSeconds: 60,
      },
      mockFetch
    );
    expect(metricAlert.firing).toBe(false);
    await deleteMailErrorAlert(errorAlert.id, mockFetch);
    await deleteMailMetricAlert(metricAlert.id, mockFetch);
    const after = await fetchMailSettings(undefined, mockFetch);
    expect(after.errorAlerts).toHaveLength(1);
    expect(after.metricAlerts).toHaveLength(1);
  });

  test("allows the same public listener on primary and child hosts", async () => {
    const mockFetch = fetcher(createMockState("demo"));
    await expect(
      attachServiceListener(
        "project-demo",
        "service-worker",
        { protocol: "tcp", publicPort: 9000, targetPort: 8080 },
        mockFetch
      )
    ).resolves.toMatchObject({
      protocol: "tcp",
      publicPort: 9000,
      serviceId: "service-worker",
    });
    await expect(
      attachServiceListener(
        "project-demo",
        "service-api",
        { protocol: "tcp", publicPort: 9000, targetPort: 9001 },
        mockFetch
      )
    ).resolves.toMatchObject({ serviceId: "service-api", targetPort: 9001 });
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
    expect(canvas.project.serviceCount).toBe(1);
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
    expect(serviceUsage.running).toBe(true);
    expect(serviceUsage.networkAvailable).toBe(true);
    expect(serviceUsageHistory.points).not.toHaveLength(0);
    expect(resolvedEnvironment.POSTGRES_URL).toContain(
      "postgresql://app_owner:mock-only-postgres-password@postgres-main.storefront.internal:5432/app"
    );
    expect(resolvedEnvironment.REDIS_URL).not.toContain("${{");
    expect(resolvedEnvironment.SENTRY_DSN).toBe(
      "http://service-api@errors-api.storefront.internal:9001/1"
    );
    expect(resolvedEnvironment.OTEL_EXPORTER_OTLP_ENDPOINT).toBe(
      "http://otel-api.storefront.internal:4318"
    );
    expect(resolvedEnvironment.OTEL_EXPORTER_OTLP_PROTOCOL).toBe(
      "http/protobuf"
    );
    expect(resolvedEnvironment.OTEL_SERVICE_NAME).toBe("api");
    expect(resolvedEnvironment.OTEL_RESOURCE_ATTRIBUTES).toContain(
      "service.namespace=storefront"
    );

    const selectedDeployment = deployments.deployments.find(
      (deployment) => deployment.id === "deployment-failed"
    );
    expect(selectedDeployment).toBeDefined();
    if (!selectedDeployment) {
      throw new Error("mock failed deployment is missing");
    }
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
    const correlatedLogs = await fetchResourceLogs(
      "project-demo",
      "service",
      "service-api",
      { traceId: "4c79f60c11214eb38604f4ae0781bfb2" },
      undefined,
      mockFetch
    );
    expect(correlatedLogs.records).toHaveLength(1);
    expect(correlatedLogs.records[0]?.spanId).toBe("8f3a0f34b17c9d20");
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

  test("mock service embeds its error console behind the telemetry route", async () => {
    const state = createMockState("demo");
    const mockFetch = fetcher(state);
    const telemetry = await fetchServiceTelemetry(
      "project-demo",
      "service-api",
      undefined,
      mockFetch
    );

    const updated = await updateServiceTelemetryPublicAccess(
      "project-demo",
      "service-api",
      {
        expectedUpdatedAt: telemetry.updatedAt,
        publicHostname: "errors.mock.local",
      },
      mockFetch
    );
    const consoleResponse = await mockFetch(
      "/api/v1/projects/project-demo/services/service-api/errors/issues?limit=100"
    );
    const consolePayload = await consoleResponse.json();
    expect(consolePayload.total).toBeGreaterThan(0);
    expect(updated.publicHostname).toBe("errors.mock.local");

    const tunneled = await updateServiceTelemetryBrowserTunnel(
      "project-demo",
      "service-api",
      {
        browserTunnelPath: "/client-report",
        expectedUpdatedAt: updated.updatedAt,
      },
      mockFetch
    );
    expect(tunneled.browserTunnelPath).toBe("/client-report");
  });

  test("mock service exposes traces with context and custom metric graphs", async () => {
    const state = createMockState("demo");
    const mockFetch = fetcher(state);
    const [traces, catalog] = await Promise.all([
      fetchServiceTraces("project-demo", "service-api", undefined, mockFetch),
      fetchServiceMetricCatalog(
        "project-demo",
        "service-api",
        undefined,
        mockFetch
      ),
    ]);
    const trace = await fetchServiceTrace(
      "project-demo",
      "service-api",
      traces[0]?.traceId ?? "",
      undefined,
      mockFetch
    );
    const aiTraces = await fetchServiceTraces(
      "project-demo",
      "service-api",
      undefined,
      mockFetch,
      { query: "invoice" }
    );
    const aiTrace = await fetchServiceTrace(
      "project-demo",
      "service-api",
      aiTraces[0]?.traceId ?? "",
      undefined,
      mockFetch
    );
    const replay = await fetchServiceReplayRecording(
      "project-demo",
      "service-api",
      "82818281828142818281828182818281",
      undefined,
      mockFetch
    );
    const chart = await createServiceMetricChart(
      "project-demo",
      "service-api",
      {
        legend: "Queue depth",
        sql: `SELECT bucket AS time, avg(value) AS value FROM metrics WHERE name = '${catalog[0]?.name ?? ""}' GROUP BY bucket`,
        title: "Queue depth",
        visualization: "area",
      },
      mockFetch
    );
    const updatedChart = await updateServiceMetricChart(
      "project-demo",
      "service-api",
      chart.id,
      {
        expectedUpdatedAt: chart.updatedAt,
        legend: "Queue depth",
        sql: "SELECT bucket AS time, max(value) AS value, attributes['region'] AS series FROM metrics GROUP BY bucket, series",
        title: "Queue depth by region",
        visualization: "line",
      },
      mockFetch
    );
    const [charts, series] = await Promise.all([
      fetchServiceMetricCharts(
        "project-demo",
        "service-api",
        undefined,
        mockFetch
      ),
      fetchServiceMetricQuery(
        "project-demo",
        "service-api",
        {
          from: Date.now() - 60_000,
          sql: updatedChart.sql,
          step: 10_000,
          to: Date.now(),
        },
        undefined,
        mockFetch
      ),
    ]);
    expect(trace.spans).toHaveLength(3);
    expect(aiTraces[0]).toMatchObject({
      aiAgentRunCount: 1,
      aiCacheReadTokens: 3180,
      aiModel: "gpt-5-mini",
      isAi: true,
      name: "POST /support/reply",
    });
    expect(aiTrace.spans).toHaveLength(6);
    expect(trace.spans[0]?.span).toMatchObject({
      replay_id: replay.replayId,
      user: { id: "customer_1042" },
    });
    expect(replay.events.length).toBeGreaterThan(0);
    expect(charts).toEqual([updatedChart]);
    expect(series.length).toBeGreaterThan(3);
    expect(series.length).toBeGreaterThan(0);

    const projectScope = {
      kind: "project",
      projectID: "project-demo",
    } as const;
    const installationScope = { kind: "installation" } as const;
    const [projectCatalog, installationCatalog] = await Promise.all([
      fetchMetricCatalog(projectScope, undefined, mockFetch),
      fetchMetricCatalog(installationScope, undefined, mockFetch),
    ]);
    const [projectChart, installationChart] = await Promise.all([
      createMetricChart(
        projectScope,
        {
          legend: "Service",
          sql: "SELECT bucket AS time, avg(value) AS value, service_id AS series FROM metrics GROUP BY bucket, series",
          title: "Project latency",
          visualization: "line",
        },
        mockFetch
      ),
      createMetricChart(
        installationScope,
        {
          legend: "Total",
          sql: "SELECT bucket AS time, sum(value) AS value FROM metrics GROUP BY bucket",
          title: "Installation requests",
          visualization: "area",
        },
        mockFetch
      ),
    ]);
    const [
      projectCharts,
      installationCharts,
      projectSeries,
      projectLogs,
      projectIssues,
      installationTraces,
    ] = await Promise.all([
      fetchMetricCharts(projectScope, undefined, mockFetch),
      fetchMetricCharts(installationScope, undefined, mockFetch),
      fetchMetricQuery(
        projectScope,
        {
          from: Date.now() - 60_000,
          sql: projectChart.sql,
          step: 10_000,
          to: Date.now(),
        },
        undefined,
        mockFetch
      ),
      fetchTelemetryLogs(projectScope, {}, undefined, mockFetch),
      fetchScopedIssues(projectScope, "", undefined, mockFetch),
      fetchTelemetryTraces(installationScope, undefined, mockFetch),
    ]);
    expect(projectCatalog).toEqual(installationCatalog);
    expect(projectCharts).toEqual([projectChart]);
    expect(installationCharts).toEqual([installationChart]);
    expect(projectSeries.length).toBeGreaterThan(0);
    expect(projectLogs.records.some((record) => record.serviceId)).toBe(true);
    expect(projectIssues.data.some((issue) => issue.serviceId)).toBe(true);
    expect(installationTraces.length).toBeGreaterThan(0);
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
    expect(Object.keys(state.serviceTelemetry)).toEqual([]);
    expect(Object.keys(state.serviceErrors)).toEqual([]);
    expect(Object.keys(state.metricCharts)).toEqual([]);
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
