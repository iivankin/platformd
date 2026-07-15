import type {
  APIToken,
  AuditEvent,
  BackupGeneration,
  BackupPolicy,
  BackupRecord,
  BackupTarget,
  Deployment,
  DiskPressure,
  Identity,
  ImageCredential,
  InfrastructureLogWindow,
  InstallationSettings,
  LogWindow,
  ManagedPostgres,
  ManagedRedis,
  Meta,
  ObjectMetadata,
  ObjectStore,
  Operation,
  Project,
  ProjectCanvas,
  RegistryCredential,
  RegistryImage,
  RegistryRepository,
  RuntimeDeployment,
  Service,
  ServiceDomain,
  Volume,
} from "../web/api";
import { mockContainerFiles } from "./container-resources";

export type MockScenario = "demo" | "empty" | "error";

export interface MockState {
  auditEvents: AuditEvent[];
  backupGenerations: Record<string, BackupGeneration[]>;
  backupHistory: Record<string, BackupRecord[]>;
  backupPolicies: BackupPolicy[];
  backupTarget: BackupTarget;
  canvases: Record<string, ProjectCanvas>;
  containerFiles: Record<string, Record<string, string>>;
  deployments: Record<string, Deployment[]>;
  diskPressure: DiskPressure;
  domains: Record<string, ServiceDomain[]>;
  identity: Identity;
  imageCredentials: Record<string, ImageCredential[]>;
  infrastructureLogs: InfrastructureLogWindow;
  logs: Record<string, LogWindow>;
  meta: Meta;
  objectMetadata: Record<string, ObjectMetadata[]>;
  objectStores: Record<string, ObjectStore>;
  operations: Record<string, Operation>;
  postgres: Record<string, ManagedPostgres>;
  projects: Project[];
  redis: Record<string, ManagedRedis>;
  runtimeDeployments: Record<string, RuntimeDeployment[]>;
  registryCredentials: Record<string, RegistryCredential[]>;
  registryHostname: string;
  registryImages: Record<string, RegistryImage[]>;
  registryRepositories: RegistryRepository[];
  scenario: MockScenario;
  sequence: number;
  services: Record<string, Service>;
  settings: InstallationSettings;
  tokens: APIToken[];
  volumes: Record<string, Volume[]>;
}

const now = Date.UTC(2026, 6, 14, 12, 0, 0);
const iso = (offsetMinutes: number) =>
  new Date(now + offsetMinutes * 60_000).toISOString();
const resourceKey = (kind: string, id: string) => `${kind}:${id}`;

const project: Project = {
  createdAt: now - 45 * 86_400_000,
  id: "project-demo",
  name: "storefront",
  objectStoreCount: 1,
  postgresCount: 1,
  redisCount: 1,
  serviceCount: 1,
  updatedAt: now - 90_000,
};

const service: Service = {
  activeConfigHash: "config-demo",
  activeDeploymentId: "deployment-demo",
  activeImageDigest: "sha256:service-demo",
  cpuMillicores: 500,
  createdAt: project.createdAt,
  enabled: true,
  environment: { LOG_LEVEL: "info" },
  healthPath: "/health",
  id: "service-api",
  imageReference: "registry.mock.local/storefront/api:stable",
  memoryMaxBytes: 536_870_912,
  name: "api",
  projectId: project.id,
  resourceReferences: [
    {
      environmentName: "DATABASE_URL",
      outputName: "DATABASE_URL",
      resourceId: "postgres-main",
      resourceKind: "postgres",
    },
    {
      environmentName: "REDIS_URL",
      outputName: "REDIS_URL",
      resourceId: "redis-cache",
      resourceKind: "redis",
    },
  ],
  secretReferences: [],
  startupTimeoutSeconds: 30,
  targetPort: 8080,
  updatedAt: now - 90_000,
  volumeMounts: [],
};

const postgres: ManagedPostgres = {
  backupCron: "0 2 * * *",
  backupEnabled: true,
  backupRetentionCount: 7,
  cpuMillicores: 750,
  createdAt: project.createdAt,
  databaseName: "app",
  hostname: "postgres-main.storefront.internal",
  id: "postgres-main",
  imageDigest: "sha256:postgres-demo",
  imageTag: "17.5",
  memoryBytes: 1_073_741_824,
  name: "main",
  ownerPassword: "mock-only-postgres-password",
  ownerUsername: "app_owner",
  port: 5432,
  projectId: project.id,
  updatedAt: now - 3_600_000,
};

const redis: ManagedRedis = {
  backupCron: "30 2 * * *",
  backupEnabled: true,
  backupRetentionCount: 5,
  cpuMillicores: 250,
  createdAt: project.createdAt,
  hostname: "redis-cache.storefront.internal",
  id: "redis-cache",
  imageDigest: "sha256:redis-demo",
  imageTag: "8.2",
  memoryBytes: 268_435_456,
  name: "cache",
  password: "mock-only-redis-password",
  port: 6379,
  projectId: project.id,
  updatedAt: now - 7_200_000,
};

const objectStore: ObjectStore = {
  accessKey: "MOCK_ONLY_ACCESS_KEY",
  backupCron: "0 3 * * *",
  backupEnabled: true,
  backupRetentionCount: 5,
  bucketName: "storefront-assets",
  corsOrigins: ["https://shop.mock.local"],
  createdAt: project.createdAt,
  credentialPermission: "read_write",
  id: "object-assets",
  internalHostname: "object-assets.storefront.internal",
  name: "assets",
  projectId: project.id,
  publicHostname: "assets.mock.local",
  region: "us-east-1",
  secret: "mock-only-object-secret",
  updatedAt: now - 7_200_000,
};

const canvas: ProjectCanvas = {
  connections: [
    {
      environmentNames: ["DATABASE_URL"],
      sourceId: service.id,
      targetId: postgres.id,
    },
    {
      environmentNames: ["REDIS_URL"],
      sourceId: service.id,
      targetId: redis.id,
    },
  ],
  project,
  resources: [
    {
      activeDeploymentId: service.activeDeploymentId,
      enabled: true,
      id: service.id,
      imageDigest: service.activeImageDigest,
      imageReference: service.imageReference,
      internalHostname: "api.storefront.internal",
      kind: "service",
      name: service.name,
      status: "running",
    },
    {
      enabled: true,
      id: postgres.id,
      imageDigest: postgres.imageDigest,
      imageReference: `postgres:${postgres.imageTag}`,
      internalHostname: postgres.hostname,
      kind: "postgres",
      name: postgres.name,
      status: "running",
    },
    {
      enabled: true,
      id: redis.id,
      imageDigest: redis.imageDigest,
      imageReference: `redis:${redis.imageTag}`,
      internalHostname: redis.hostname,
      kind: "redis",
      name: redis.name,
      status: "running",
    },
    {
      bucketName: objectStore.bucketName,
      enabled: true,
      id: objectStore.id,
      internalHostname: objectStore.internalHostname,
      kind: "object_store",
      name: objectStore.name,
      status: "running",
    },
  ],
};

const repository: RegistryRepository = {
  backupCron: "0 4 * * *",
  backupEnabled: true,
  backupRetentionCount: 5,
  blobCount: 6,
  createdAt: now - 30 * 86_400_000,
  id: "repository-api",
  lastPushedAt: now - 45 * 60_000,
  manifestCount: 2,
  name: "storefront/api",
  publicPull: false,
  referencedBlobBytes: 188_743_680,
  tagCount: 3,
  totalBlobBytes: 201_326_592,
  updatedAt: now - 45 * 60_000,
};

const registryImage: RegistryImage = {
  blobDigests: ["sha256:config-demo", "sha256:layer-demo"],
  digest: "sha256:image-demo",
  manifest: {
    config: {
      digest: "sha256:config-demo",
      mediaType: "application/vnd.oci.image.config.v1+json",
    },
    schemaVersion: 2,
  },
  manifestSize: 1432,
  mediaType: "application/vnd.oci.image.manifest.v1+json",
  platforms: [{ architecture: "amd64", os: "linux" }],
  pushedAt: now - 45 * 60_000,
  referencedBlobBytes: 94_371_840,
  tags: ["stable", "2026.07.14"],
};

const policies: BackupPolicy[] = [
  ["postgres", postgres.id, "0 2 * * *", 7],
  ["redis", redis.id, "30 2 * * *", 5],
  ["object_store", objectStore.id, "0 3 * * *", 5],
  ["registry", repository.id, "0 4 * * *", 5],
].map(([resourceKind, resourceId, cron, retentionCount], index) => ({
  cron: String(cron),
  enabled: true,
  nextRunAt: now + (index + 1) * 3_600_000,
  resourceId: String(resourceId),
  resourceKind: resourceKind as BackupPolicy["resourceKind"],
  retentionCount: Number(retentionCount),
}));

const generation = (id: string): BackupGeneration => ({
  completedAt: now - 86_400_000,
  generationId: `generation-${id}`,
  plaintextSize: 52_428_800,
  remoteSize: 18_874_368,
});

const makeEmptyState = (scenario: MockScenario): MockState => ({
  auditEvents: [],
  backupGenerations: {},
  backupHistory: {},
  backupPolicies: [],
  backupTarget: { configured: false },
  canvases: {},
  containerFiles: {},
  deployments: {},
  diskPressure: {
    availableBytes: 171_798_691_840,
    availableInodes: 8_300_000,
    byteBasisPoints: 2800,
    checkedAt: now,
    inodeBasisPoints: 1300,
    level: "normal",
    reservePresent: true,
    totalBytes: 238_370_684_928,
    totalInodes: 9_500_000,
  },
  domains: {},
  identity: { email: "developer@mock.local", subject: "mock-developer" },
  imageCredentials: {},
  infrastructureLogs: { records: [], truncated: false },
  logs: {},
  meta: {
    architecture: "arm64",
    os: "darwin",
    status: "ready",
    version: "0.1.0-mock",
  },
  objectMetadata: {},
  objectStores: {},
  operations: {},
  postgres: {},
  projects: [],
  redis: {},
  registryCredentials: {},
  registryHostname: "",
  registryImages: {},
  registryRepositories: [],
  runtimeDeployments: {},
  scenario,
  sequence: 100,
  services: {},
  settings: {
    accessAudience: "mock-audience",
    accessTeamDomain: "mock-team.cloudflareaccess.com",
    adminHostname: "admin.mock.local",
    automationHostname: "",
    certificates: [],
    installationId: "installation-mock",
  },
  tokens: [],
  volumes: {},
});

export const createMockState = (scenario: MockScenario): MockState => {
  const state = makeEmptyState(scenario);
  if (scenario === "empty") {
    return state;
  }

  state.projects = [project];
  state.canvases[project.id] = canvas;
  state.services[service.id] = service;
  state.postgres[postgres.id] = postgres;
  state.redis[redis.id] = redis;
  state.objectStores[objectStore.id] = objectStore;
  state.containerFiles["service:service-api"] = mockContainerFiles("service");
  state.containerFiles["postgres:postgres-main"] =
    mockContainerFiles("postgres");
  state.containerFiles["redis:redis-cache"] = mockContainerFiles("redis");
  state.imageCredentials[project.id] = [
    {
      createdAt: now - 30 * 86_400_000,
      id: "image-credential-demo",
      name: "internal-registry",
      registryHost: "registry.mock.local",
      username: "robot-storefront",
    },
  ];
  state.deployments[service.id] = [
    {
      createdAt: now - 90_000,
      finishedAt: now - 70_000,
      id: "deployment-demo",
      imageDigest: "sha256:service-demo",
      serviceConfigHash: "config-demo",
      serviceId: service.id,
      snapshot: {
        cpuMillicores: service.cpuMillicores,
        environment: service.environment,
        healthPath: service.healthPath,
        imageReference: service.imageReference,
        memoryMaxBytes: service.memoryMaxBytes,
        resourceReferences: service.resourceReferences,
        secretReferences: [],
        startupTimeoutSeconds: service.startupTimeoutSeconds,
        targetPort: service.targetPort,
        volumeMounts: [],
      },
      status: "succeeded",
    },
    {
      createdAt: now - 86_400_000,
      errorCode: "readiness_failed",
      errorMessage: "Health check did not become ready before the timeout.",
      finishedAt: now - 86_360_000,
      id: "deployment-failed",
      imageDigest: "sha256:service-failed",
      serviceConfigHash: "config-failed",
      serviceId: service.id,
      snapshot: {
        cpuMillicores: service.cpuMillicores,
        environment: service.environment,
        healthPath: service.healthPath,
        imageReference: "registry.mock.local/storefront/api:candidate",
        memoryMaxBytes: service.memoryMaxBytes,
        resourceReferences: service.resourceReferences,
        secretReferences: [],
        startupTimeoutSeconds: service.startupTimeoutSeconds,
        targetPort: service.targetPort,
        volumeMounts: [],
      },
      status: "failed",
    },
    {
      createdAt: now - 172_800_000,
      finishedAt: now - 172_775_000,
      id: "deployment-previous",
      imageDigest: "sha256:service-previous",
      serviceConfigHash: "config-previous",
      serviceId: service.id,
      snapshot: {
        cpuMillicores: 400,
        environment: { LOG_LEVEL: "warn" },
        healthPath: service.healthPath,
        imageReference: "registry.mock.local/storefront/api:previous",
        memoryMaxBytes: service.memoryMaxBytes,
        resourceReferences: service.resourceReferences,
        secretReferences: [],
        startupTimeoutSeconds: service.startupTimeoutSeconds,
        targetPort: service.targetPort,
        volumeMounts: [],
      },
      status: "succeeded",
    },
  ];
  state.runtimeDeployments[postgres.id] = [
    {
      active: true,
      createdAt: now - 120_000,
      finishedAt: now - 100_000,
      id: "postgres-deployment-current",
      imageDigest: postgres.imageDigest,
      imageTag: postgres.imageTag,
      resourceId: postgres.id,
      resourceKind: "postgres",
      status: "succeeded",
    },
    {
      active: false,
      createdAt: now - 7 * 86_400_000,
      finishedAt: now - 7 * 86_400_000 + 18_000,
      id: "postgres-deployment-previous",
      imageDigest: "sha256:postgres-previous",
      imageTag: "16.9",
      resourceId: postgres.id,
      resourceKind: "postgres",
      status: "succeeded",
    },
  ];
  state.runtimeDeployments[redis.id] = [
    {
      active: true,
      createdAt: now - 180_000,
      finishedAt: now - 165_000,
      id: "redis-deployment-current",
      imageDigest: redis.imageDigest,
      imageTag: redis.imageTag,
      resourceId: redis.id,
      resourceKind: "redis",
      status: "succeeded",
    },
    {
      active: false,
      createdAt: now - 86_400_000,
      errorCode: "start_failed",
      errorMessage: "Candidate exited before it accepted connections.",
      finishedAt: now - 86_380_000,
      id: "redis-deployment-failed",
      imageDigest: "sha256:redis-candidate",
      imageTag: "8.3-rc",
      resourceId: redis.id,
      resourceKind: "redis",
      status: "failed",
    },
  ];
  state.domains[service.id] = [
    {
      createdAt: now - 20 * 86_400_000,
      hostname: "shop.mock.local",
      projectId: project.id,
      projectName: project.name,
      serviceId: service.id,
      serviceName: service.name,
    },
  ];
  state.volumes[service.id] = [];
  state.logs[service.id] = {
    records: [
      {
        attemptId: "attempt-demo",
        deploymentId: "deployment-demo",
        stream: "stdout",
        text: "HTTP server listening on :8080",
        timestamp: iso(-2),
      },
      {
        attemptId: "attempt-demo",
        deploymentId: "deployment-demo",
        stream: "stdout",
        text: "GET /health 200 2ms",
        timestamp: iso(-1),
      },
      {
        attemptId: "attempt-failed",
        deploymentId: "deployment-failed",
        stream: "stderr",
        text: "Health check failed: connection refused",
        timestamp: iso(-1440),
      },
      {
        attemptId: "attempt-previous",
        deploymentId: "deployment-previous",
        stream: "stdout",
        text: "Previous release started on :8080",
        timestamp: iso(-2880),
      },
    ],
    truncated: false,
  };
  state.logs[redis.id] = {
    records: [
      {
        attemptId: "redis-runtime-demo",
        deploymentId: "redis-deployment-current",
        stream: "stdout",
        text: "Ready to accept connections tcp",
        timestamp: iso(-4),
      },
    ],
    truncated: false,
  };
  state.logs[postgres.id] = {
    records: [
      {
        attemptId: "postgres-runtime-demo",
        deploymentId: "postgres-deployment-current",
        stream: "stderr",
        text: "database system is ready to accept connections",
        timestamp: iso(-3),
      },
    ],
    truncated: false,
  };
  state.logs[objectStore.id] = {
    records: [
      {
        attemptId: "object-activity-demo",
        deploymentId: objectStore.id,
        stream: "stdout",
        text: "object_store.create succeeded",
        timestamp: iso(-5),
      },
    ],
    truncated: false,
  };
  state.objectMetadata[objectStore.id] = [
    {
      contentType: "application/json",
      createdAt: now - 86_400_000,
      etag: "mock-etag-catalog",
      objectKey: "catalog/products.json",
      size: 2048,
      updatedAt: now - 3_600_000,
    },
    {
      contentType: "image/webp",
      createdAt: now - 172_800_000,
      etag: "mock-etag-hero",
      objectKey: "images/hero.webp",
      size: 421_888,
      updatedAt: now - 172_800_000,
    },
  ];
  state.backupTarget = {
    accessKeyId: "MOCK_ACCESS_KEY",
    bucket: "platformd-mock-backups",
    configured: true,
    createdAt: now - 30 * 86_400_000,
    endpoint: "https://s3.mock.local",
    prefix: "demo-installation",
    region: "mock-region-1",
    updatedAt: now - 86_400_000,
  };
  state.backupPolicies = policies;
  for (const policy of policies) {
    const key = resourceKey(policy.resourceKind, policy.resourceId);
    state.backupGenerations[key] = [generation(policy.resourceId)];
    state.backupHistory[key] = [
      {
        finishedAt: now - 86_340_000,
        generationId: `generation-${policy.resourceId}`,
        id: `backup-${policy.resourceId}`,
        resourceId: policy.resourceId,
        resourceKind: policy.resourceKind,
        sizeBytes: 18_874_368,
        startedAt: now - 86_400_000,
        status: "succeeded",
      },
    ];
  }
  state.registryHostname = "registry.mock.local";
  state.registryRepositories = [repository];
  state.registryImages[repository.id] = [registryImage];
  state.registryCredentials[repository.id] = [
    {
      createdAt: now - 20 * 86_400_000,
      id: "registry-credential-demo",
      lastUsedAt: now - 45 * 60_000,
      name: "deployer",
      permission: "pull_push",
      secretAvailable: false,
      username: "prg_legacy_mock_credential",
    },
  ];
  state.tokens = [
    {
      createdAt: now - 14 * 86_400_000,
      id: "token-readonly-demo",
      lastUsedAt: now - 15 * 60_000,
      name: "reporting",
      role: "read",
    },
    {
      createdAt: now - 7 * 86_400_000,
      id: "token-deploy-demo",
      name: "deploy-storefront",
      projectId: project.id,
      role: "admin",
    },
  ];
  state.settings.automationHostname = "api.mock.local";
  state.settings.certificates = [
    {
      createdAt: now - 30 * 86_400_000,
      dnsNames: ["*.mock.local", "mock.local"],
      id: "certificate-demo",
    },
  ];
  state.infrastructureLogs = {
    records: [
      {
        cursor: "journal-1",
        identifier: "platformd",
        message: "control plane ready",
        pid: "4200",
        priority: 6,
        timestamp: iso(-5),
      },
      {
        cursor: "journal-2",
        identifier: "platformd",
        message: "backup completed for postgres-main",
        pid: "4200",
        priority: 5,
        timestamp: iso(-3),
      },
    ],
    truncated: false,
  };
  state.auditEvents = [
    {
      action: "service.update",
      actorId: "developer@mock.local",
      actorKind: "access",
      createdAt: now - 90_000,
      id: "audit-service-update",
      metadata: { enabled: true },
      requestCorrelationId: "request-demo-1",
      result: "succeeded",
      targetId: service.id,
      targetKind: "service",
    },
    {
      action: "backup.run",
      actorId: "scheduler",
      actorKind: "system",
      createdAt: now - 86_400_000,
      id: "audit-backup-run",
      metadata: {},
      result: "succeeded",
      targetId: postgres.id,
      targetKind: "postgres",
    },
  ];
  return state;
};

export const nextMockID = (state: MockState, prefix: string) => {
  state.sequence += 1;
  return `${prefix}-mock-${state.sequence}`;
};

export const mockNow = () => now;

export const mockResourceKey = resourceKey;
