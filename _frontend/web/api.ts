import { z } from "zod";

const metaSchema = z.object({
  architecture: z.string(),
  os: z.string(),
  status: z.enum(["bootstrapping", "ready", "recovery"]),
  version: z.string(),
});

export type Meta = z.infer<typeof metaSchema>;

const identitySchema = z.object({
  email: z.email(),
  subject: z.string().min(1),
});

export type Identity = z.infer<typeof identitySchema>;

const projectSchema = z.object({
  createdAt: z.number().int().nonnegative(),
  id: z.string().min(1),
  name: z.string().min(1),
  objectStoreCount: z.number().int().nonnegative(),
  postgresCount: z.number().int().nonnegative(),
  redisCount: z.number().int().nonnegative(),
  serviceCount: z.number().int().nonnegative(),
  updatedAt: z.number().int().nonnegative(),
});

const projectsSchema = z.array(projectSchema);
const serviceDomainSchema = z.object({
  createdAt: z.number().int().positive(),
  hostname: z.string().min(1),
  projectId: z.string().min(1).optional(),
  projectName: z.string().min(1).optional(),
  serviceId: z.string().min(1),
  serviceName: z.string().min(1).optional(),
});
const serviceDomainsSchema = z.object({
  domains: z.array(serviceDomainSchema),
});
const apiErrorSchema = z.object({
  error: z.object({
    code: z.string(),
    domain: serviceDomainSchema.optional(),
    message: z.string(),
  }),
});

export type Project = z.infer<typeof projectSchema>;
export type ServiceDomain = z.infer<typeof serviceDomainSchema>;

const apiTokenSchema = z.object({
  createdAt: z.number().int().positive(),
  id: z.string().min(1),
  lastUsedAt: z.number().int().positive().optional(),
  name: z.string().min(1),
  projectId: z.string().min(1).optional(),
  revokedAt: z.number().int().positive().optional(),
  role: z.enum(["read", "admin"]),
  token: z.string().min(1).optional(),
});
const apiTokensSchema = z.object({ tokens: z.array(apiTokenSchema) });
export type APIToken = z.infer<typeof apiTokenSchema>;

const canvasResourceSchema = z.object({
  activeDeploymentId: z.string().min(1).optional(),
  bucketName: z.string().optional(),
  enabled: z.boolean(),
  id: z.string().min(1),
  imageDigest: z.string().min(1).optional(),
  imageReference: z.string().optional(),
  internalHostname: z.string().min(1),
  kind: z.enum(["service", "postgres", "redis", "object_store"]),
  name: z.string().min(1),
  status: z.enum(["degraded", "disabled", "failed", "pending", "running"]),
  statusMessage: z.string().optional(),
});

const canvasConnectionSchema = z.object({
  environmentNames: z.array(z.string().min(1)),
  sourceId: z.string().min(1),
  targetId: z.string().min(1),
});

const projectCanvasSchema = z.object({
  connections: z.array(canvasConnectionSchema),
  project: projectSchema,
  resources: z.array(canvasResourceSchema),
});

export type ProjectCanvas = z.infer<typeof projectCanvasSchema>;

const imageCredentialSchema = z.object({
  createdAt: z.number().int().nonnegative(),
  id: z.string().min(1),
  name: z.string().min(1),
  registryHost: z.string().min(1),
  username: z.string().min(1),
});

const imageCredentialsSchema = z.array(imageCredentialSchema);
export type ImageCredential = z.infer<typeof imageCredentialSchema>;

const serviceSchema = z.object({
  activeConfigHash: z.string().min(1).optional(),
  activeDeploymentId: z.string().min(1).optional(),
  activeImageDigest: z.string().min(1).optional(),
  args: z.array(z.string()).optional(),
  command: z.array(z.string()).optional(),
  cpuMillicores: z.number().int().nonnegative().optional(),
  createdAt: z.number().int().positive(),
  enabled: z.boolean(),
  environment: z.record(z.string(), z.string()),
  healthPath: z.string().optional(),
  id: z.string().min(1),
  imageCredentialId: z.string().min(1).optional(),
  imageReference: z.string().min(1),
  memoryMaxBytes: z.number().int().nonnegative().optional(),
  name: z.string().min(1),
  projectId: z.string().min(1),
  secretReferences: z.array(
    z.object({
      environmentName: z.string().min(1),
      secretId: z.string().min(1),
    })
  ),
  startupTimeoutSeconds: z.number().int().positive(),
  targetPort: z.number().int().min(1).max(65_535).optional(),
  updatedAt: z.number().int().positive(),
  volumeMounts: z.array(
    z.object({ containerPath: z.string().min(1), volumeId: z.string().min(1) })
  ),
});

export type Service = z.infer<typeof serviceSchema>;

const volumeSchema = z.object({
  createdAt: z.number().int().positive(),
  id: z.string().min(1),
  name: z.string().min(1),
  ownerGid: z.number().int().nonnegative(),
  ownerUid: z.number().int().nonnegative(),
  projectId: z.string().min(1),
  serviceId: z.string().min(1),
});

const volumeOwnerSuggestionSchema = z.object({
  exactNumeric: z.boolean(),
  imageUser: z.string(),
  ownerGid: z.number().int().nonnegative(),
  ownerUid: z.number().int().nonnegative(),
});

export type Volume = z.infer<typeof volumeSchema>;
export type VolumeOwnerSuggestion = z.infer<typeof volumeOwnerSuggestionSchema>;

export interface CreateVolumeInput {
  name: string;
  ownerGid: number;
  ownerUid: number;
}

export interface CreateServiceInput {
  environment: Record<string, string>;
  healthPath?: string;
  imageCredentialId?: string;
  imageReference: string;
  name: string;
  targetPort?: number;
}

export interface UpdateServiceInput {
  args?: string[];
  command?: string[];
  cpuMillicores?: number;
  enabled: boolean;
  environment: Record<string, string>;
  expectedUpdatedAt: number;
  healthPath?: string;
  imageCredentialId?: string;
  imageReference: string;
  memoryMaxBytes?: number;
  secretReferences: Service["secretReferences"];
  startupTimeoutSeconds: number;
  targetPort?: number;
  volumeMounts: Service["volumeMounts"];
}

const deploymentSchema = z.object({
  createdAt: z.number().int().positive(),
  errorCode: z.string().optional(),
  errorMessage: z.string().optional(),
  finishedAt: z.number().int().positive().optional(),
  id: z.string().min(1),
  imageDigest: z.string().min(1),
  serviceConfigHash: z.string().min(1),
  serviceId: z.string().min(1),
  snapshot: serviceSchema.pick({
    args: true,
    command: true,
    cpuMillicores: true,
    environment: true,
    healthPath: true,
    imageCredentialId: true,
    imageReference: true,
    memoryMaxBytes: true,
    secretReferences: true,
    startupTimeoutSeconds: true,
    targetPort: true,
    volumeMounts: true,
  }),
  status: z.enum(["failed", "interrupted", "running", "succeeded"]),
});

const deploymentPageSchema = z.object({
  deployments: z.array(deploymentSchema),
  nextCursor: z.string().min(1).optional(),
});

export type Deployment = z.infer<typeof deploymentSchema>;
export type DeploymentPage = z.infer<typeof deploymentPageSchema>;

const logRecordSchema = z.object({
  attemptId: z.string().min(1),
  deploymentId: z.string().min(1),
  partial: z.boolean().optional(),
  stream: z.enum(["stdout", "stderr"]),
  text: z.string(),
  timestamp: z.iso.datetime({ offset: true }),
  truncated: z.boolean().optional(),
});

const logWindowSchema = z.object({
  records: z.array(logRecordSchema),
  truncated: z.boolean(),
});

const logStreamMessageSchema = z.discriminatedUnion("type", [
  z.object({
    records: z.array(logRecordSchema),
    truncated: z.boolean().optional().default(false),
    type: z.literal("snapshot"),
  }),
  z.object({ records: z.array(logRecordSchema), type: z.literal("records") }),
  z.object({ type: z.literal("gap") }),
]);

export type LogRecord = z.infer<typeof logRecordSchema>;
export type LogStreamMessage = z.infer<typeof logStreamMessageSchema>;
export type LogWindow = z.infer<typeof logWindowSchema>;

export const parseLogStreamMessage = (value: unknown): LogStreamMessage =>
  logStreamMessageSchema.parse(value);

const terminalShellsSchema = z.object({
  shells: z.array(z.enum(["/bin/sh", "/bin/bash"])),
});

const diskPressureSchema = z.object({
  availableBytes: z.number().int().nonnegative(),
  availableInodes: z.number().int().nonnegative(),
  byteBasisPoints: z.number().int().min(0).max(10_000),
  checkedAt: z.number().int().positive(),
  inodeBasisPoints: z.number().int().min(0).max(10_000),
  level: z.enum(["normal", "low", "critical", "emergency"]),
  reservePresent: z.boolean(),
  totalBytes: z.number().int().positive(),
  totalInodes: z.number().int().nonnegative(),
});

export type DiskPressure = z.infer<typeof diskPressureSchema>;

const selfUpdateResultSchema = z.object({
  previousVersion: z.string().min(1),
  targetVersion: z.string().min(1),
});

export type SelfUpdateResult = z.infer<typeof selfUpdateResultSchema>;

const auditEventSchema = z.object({
  action: z.string().min(1),
  actorId: z.string().min(1),
  actorKind: z.enum(["access", "token", "system", "local_root"]),
  createdAt: z.number().int().positive(),
  id: z.string().min(1),
  metadata: z.record(z.string(), z.unknown()),
  requestCorrelationId: z.string().min(1).optional(),
  result: z.enum(["succeeded", "failed"]),
  targetId: z.string().min(1),
  targetKind: z.string().min(1),
});

const auditPageSchema = z.object({
  events: z.array(auditEventSchema),
  nextCursor: z.string().min(1).optional(),
});

export type AuditEvent = z.infer<typeof auditEventSchema>;
export type AuditPage = z.infer<typeof auditPageSchema>;

const managedImagePlatformSchema = z.object({
  architecture: z.string().min(1),
  digest: z.string().min(1),
  os: z.string().min(1),
  sizeBytes: z.number().int().nonnegative(),
});

const managedImageTagSchema = z.object({
  lastUpdated: z.iso.datetime({ offset: true }),
  name: z.string().min(1),
  platforms: z.array(managedImagePlatformSchema),
});

const managedImagePageSchema = z.object({
  nextPage: z.number().int().positive().optional(),
  page: z.number().int().positive(),
  pageSize: z.number().int().min(1).max(100),
  previousPage: z.number().int().positive().optional(),
  rateLimitRemaining: z.number().int().nonnegative().optional(),
  rateLimitReset: z.number().int().nonnegative().optional(),
  tags: z.array(managedImageTagSchema),
  total: z.number().int().nonnegative(),
});

export type ManagedImageEngine = "postgres" | "redis";
export type ManagedImageTag = z.infer<typeof managedImageTagSchema>;
export type ManagedImagePage = z.infer<typeof managedImagePageSchema>;

const managedRedisSchema = z.object({
  backupCron: z.string().optional(),
  backupEnabled: z.boolean(),
  backupRetentionCount: z.number().int().min(1).max(100),
  cpuMillicores: z.number().int().nonnegative().optional(),
  createdAt: z.number().int().positive(),
  hostname: z.string().min(1),
  id: z.string().min(1),
  imageDigest: z.string().min(1),
  imageTag: z.string().min(1),
  memoryBytes: z.number().int().nonnegative().optional(),
  name: z.string().min(1),
  password: z.string().min(1).optional(),
  port: z.literal(6379),
  projectId: z.string().min(1),
  updatedAt: z.number().int().positive(),
});

const redisKeySchema = z.object({
  expiresInMillis: z.number().int().nonnegative().optional(),
  keyBase64: z.string(),
  keyText: z.string().optional(),
  sizeBytes: z.number().int().nonnegative(),
  type: z.string().min(1),
});

const redisKeyPageSchema = z.object({
  keys: z.array(redisKeySchema),
  nextCursor: z.string().regex(/^\d+$/u),
});

const redisPreviewSchema = z.object({
  items: z.array(
    z.object({
      values: z.array(
        z.object({ base64: z.string(), text: z.string().optional() })
      ),
    })
  ),
  length: z.number().int().nonnegative(),
  nextCursor: z.string().regex(/^\d+$/u),
  truncated: z.boolean(),
  type: z.string().min(1),
});

export type ManagedRedis = z.infer<typeof managedRedisSchema>;
export type RedisKey = z.infer<typeof redisKeySchema>;
export type RedisKeyPage = z.infer<typeof redisKeyPageSchema>;
export type RedisPreview = z.infer<typeof redisPreviewSchema>;

export interface CreateManagedRedisInput {
  cpuMillicores?: number;
  imageTag: string;
  memoryBytes?: number;
  name: string;
}

export type RedisMutationOperation =
  | "hash_delete"
  | "hash_set"
  | "key_delete"
  | "list_push_left"
  | "list_push_right"
  | "list_remove"
  | "list_set"
  | "set_add"
  | "set_remove"
  | "stream_add"
  | "stream_delete"
  | "string_set"
  | "ttl_clear"
  | "ttl_set"
  | "zset_add"
  | "zset_remove";

export interface RedisMutationInput {
  count?: number;
  field?: string;
  fields?: { field: string; value: string }[];
  index?: number;
  key: string;
  member?: string;
  operation: RedisMutationOperation;
  score?: number;
  streamId?: string;
  ttlMillis?: number;
  value?: string;
}

const redisMutationResultSchema = z.object({
  affected: z.number().int().nonnegative(),
  auditRecorded: z.boolean(),
  streamId: z.string(),
});
export type RedisMutationResult = z.infer<typeof redisMutationResultSchema>;

const managedPostgresSchema = z.object({
  backupCron: z.string().optional(),
  backupEnabled: z.boolean(),
  backupRetentionCount: z.number().int().min(1).max(100),
  cpuMillicores: z.number().int().nonnegative().optional(),
  createdAt: z.number().int().positive(),
  databaseName: z.string().min(1),
  hostname: z.string().min(1),
  id: z.string().min(1),
  imageDigest: z.string().min(1),
  imageTag: z.string().min(1),
  memoryBytes: z.number().int().nonnegative().optional(),
  name: z.string().min(1),
  ownerPassword: z.string().min(1).optional(),
  ownerUsername: z.string().min(1),
  port: z.literal(5432),
  projectId: z.string().min(1),
  updatedAt: z.number().int().positive(),
});

const postgresCellSchema = z.object({
  base64: z.string().optional(),
  null: z.boolean().optional(),
  text: z.string().optional(),
});

const postgresQueryResultSchema = z.object({
  auditRecorded: z.boolean(),
  statements: z.array(
    z.object({
      columns: z.array(
        z.object({ name: z.string(), typeOid: z.number().int().nonnegative() })
      ),
      commandTag: z.string(),
      rows: z.array(z.array(postgresCellSchema)),
      truncated: z.boolean(),
    })
  ),
  truncated: z.boolean(),
});

export type ManagedPostgres = z.infer<typeof managedPostgresSchema>;
export type PostgresQueryResult = z.infer<typeof postgresQueryResultSchema>;

export interface CreateManagedPostgresInput {
  cpuMillicores?: number;
  imageTag: string;
  memoryBytes?: number;
  name: string;
}

const objectStoreSchema = z.object({
  accessKey: z.string().min(1).optional(),
  backupCron: z.string().optional(),
  backupEnabled: z.boolean(),
  backupRetentionCount: z.number().int().min(1).max(100),
  bucketName: z.string().min(3),
  corsOrigins: z.array(z.string()),
  createdAt: z.number().int().positive(),
  credentialPermission: z.enum(["read", "read_write"]).optional(),
  id: z.string().min(1),
  internalHostname: z.string().min(1),
  name: z.string().min(1),
  projectId: z.string().min(1),
  publicHostname: z.string().min(1).optional(),
  region: z.literal("us-east-1"),
  secret: z.string().min(1).optional(),
  updatedAt: z.number().int().positive(),
});

const objectMetadataSchema = z.object({
  contentType: z.string().optional(),
  createdAt: z.number().int().positive(),
  etag: z.string().min(1),
  objectKey: z.string().min(1),
  size: z.number().int().nonnegative(),
  updatedAt: z.number().int().positive(),
});

const objectPageSchema = z.object({
  nextContinuationToken: z.string(),
  objects: z.array(objectMetadataSchema),
});

const objectPreviewSchema = z.object({
  allowed: z.boolean(),
  base64: z.string().optional(),
  metadata: objectMetadataSchema,
  text: z.string().optional(),
});

export type ObjectStore = z.infer<typeof objectStoreSchema>;
export type ObjectMetadata = z.infer<typeof objectMetadataSchema>;
export type ObjectPage = z.infer<typeof objectPageSchema>;
export type ObjectPreview = z.infer<typeof objectPreviewSchema>;

export interface CreateObjectStoreInput {
  bucketName: string;
  corsOrigins: string[];
  name: string;
  publicHostname?: string;
}

const registrySettingsSchema = z.object({ hostname: z.string() });
const registryRepositorySchema = z.object({
  backupCron: z.string().optional(),
  backupEnabled: z.boolean(),
  backupRetentionCount: z.number().int().min(1).max(100),
  blobCount: z.number().int().nonnegative(),
  createdAt: z.number().int().positive(),
  credentialName: z.string().min(1).optional(),
  credentialPermission: z.enum(["pull", "pull_push"]).optional(),
  id: z.string().min(1),
  lastPushedAt: z.number().int().positive().optional(),
  manifestCount: z.number().int().nonnegative(),
  name: z.string().min(1),
  publicPull: z.boolean(),
  referencedBlobBytes: z.number().int().nonnegative(),
  secret: z.string().min(1).optional(),
  tagCount: z.number().int().nonnegative(),
  totalBlobBytes: z.number().int().nonnegative(),
  updatedAt: z.number().int().positive(),
  username: z.string().min(1).optional(),
});
const registryRepositoriesSchema = z.object({
  repositories: z.array(registryRepositorySchema),
});
const registryPlatformSchema = z.object({
  architecture: z.string(),
  os: z.string(),
  variant: z.string().optional(),
});
const registryImageSchema = z.object({
  blobDigests: z.array(z.string()),
  digest: z.string().min(1),
  manifest: z.record(z.string(), z.unknown()).optional(),
  manifestSize: z.number().int().positive(),
  mediaType: z.string().min(1),
  platforms: z.array(registryPlatformSchema),
  pushedAt: z.number().int().positive(),
  referencedBlobBytes: z.number().int().nonnegative(),
  tags: z.array(z.string()),
});
const registryImagesSchema = z.object({
  images: z.array(registryImageSchema),
  nextCursor: z.string(),
});
const registryCredentialSchema = z.object({
  createdAt: z.number().int().positive(),
  id: z.string().min(1),
  lastUsedAt: z.number().int().positive().optional(),
  name: z.string().min(1),
  permission: z.enum(["pull", "pull_push"]),
  secret: z.string().min(1).optional(),
  username: z.string().min(1).optional(),
});
const registryCredentialsSchema = z.object({
  credentials: z.array(registryCredentialSchema),
});
const registryCleanupSchema = z.object({
  blobCount: z.number().int().nonnegative(),
  bytes: z.number().int().nonnegative(),
  deleted: z.boolean(),
  previewDigests: z.array(z.string()),
  previewTruncated: z.boolean(),
});

export type RegistrySettings = z.infer<typeof registrySettingsSchema>;
export type RegistryRepository = z.infer<typeof registryRepositorySchema>;
export type RegistryImage = z.infer<typeof registryImageSchema>;
export type RegistryCredential = z.infer<typeof registryCredentialSchema>;
export type RegistryCleanup = z.infer<typeof registryCleanupSchema>;

export interface CreateRegistryRepositoryInput {
  credentialName: string;
  credentialPermission: "pull" | "pull_push";
  name: string;
  publicPull: boolean;
}

const backupTargetSchema = z.object({
  accessKeyId: z.string().min(1).optional(),
  bucket: z.string().min(1).optional(),
  configured: z.boolean(),
  createdAt: z.number().int().positive().optional(),
  endpoint: z.string().min(1).optional(),
  prefix: z.string().optional(),
  region: z.string().min(1).optional(),
  updatedAt: z.number().int().positive().optional(),
});

export type BackupTarget = z.infer<typeof backupTargetSchema>;

export interface SetBackupTargetInput {
  accessKeyId: string;
  bucket: string;
  endpoint: string;
  prefix: string;
  region: string;
  secretAccessKey: string;
}

const backupGenerationSchema = z.object({
  completedAt: z.number().int().positive(),
  generationId: z.string().min(1),
  plaintextSize: z.number().int().nonnegative(),
  remoteSize: z.number().int().nonnegative(),
});

const backupGenerationsSchema = z.object({
  generations: z.array(backupGenerationSchema),
});

const operationSchema = z.object({
  errorCode: z.string().optional(),
  errorMessage: z.string().optional(),
  finishedAt: z.number().int().positive().optional(),
  id: z.string().min(1),
  kind: z.string().min(1),
  progress: z.string().optional(),
  startedAt: z.number().int().positive(),
  status: z.enum(["failed", "interrupted", "running", "succeeded"]),
  targetId: z.string().min(1),
});

const databaseVersionPreviewSchema = z.object({
  availableFreeBytes: z.number().int().nonnegative(),
  blocker: z.enum(["same_digest", "insufficient_space"]).optional(),
  currentDataBytes: z.number().int().nonnegative(),
  ready: z.boolean(),
  requiredFreeBytes: z.number().int().nonnegative(),
  sourceDigest: z.string().min(1),
  sourceTag: z.string().min(1),
  targetDigest: z.string().min(1),
  targetTag: z.string().min(1),
});

const databaseVersionStartSchema = databaseVersionPreviewSchema
  .pick({
    sourceDigest: true,
    sourceTag: true,
    targetDigest: true,
    targetTag: true,
  })
  .extend({ operation: operationSchema });

const recoveryResourceKindSchema = z.enum([
  "object_store",
  "postgres",
  "redis",
  "registry",
]);

const backupPolicySchema = z.object({
  cron: z.string().optional(),
  enabled: z.boolean(),
  resourceId: z.string().min(1),
  resourceKind: recoveryResourceKindSchema,
  retentionCount: z.number().int().min(1).max(100),
});

const backupPoliciesSchema = z.object({
  policies: z.array(backupPolicySchema),
});

const backupRecordSchema = z.object({
  errorCode: z.string().optional(),
  errorMessage: z.string().optional(),
  finishedAt: z.number().int().positive().optional(),
  generationId: z.string().min(1).optional(),
  id: z.string().min(1),
  resourceId: z.string().min(1),
  resourceKind: recoveryResourceKindSchema,
  scheduledOccurrence: z.number().int().positive().optional(),
  sizeBytes: z.number().int().nonnegative().optional(),
  startedAt: z.number().int().positive(),
  status: z.enum(["failed", "interrupted", "running", "succeeded"]),
});

const backupHistorySchema = z.object({ backups: z.array(backupRecordSchema) });

const recoveryResourceSchema = z.object({
  generationId: z.string().min(1).optional(),
  resourceId: z.string().min(1),
  resourceKind: recoveryResourceKindSchema,
  sourceCompletedAt: z.number().int().positive().optional(),
  status: z.enum(["empty", "pending", "restored"]),
});

const recoveryStatusSchema = z.object({
  lastError: z.string().optional(),
  resources: z.array(recoveryResourceSchema),
});

export type BackupGeneration = z.infer<typeof backupGenerationSchema>;
export type BackupPolicy = z.infer<typeof backupPolicySchema>;
export type BackupRecord = z.infer<typeof backupRecordSchema>;
export type Operation = z.infer<typeof operationSchema>;
export type DatabaseVersionPreview = z.infer<
  typeof databaseVersionPreviewSchema
>;
export type DatabaseVersionStart = z.infer<typeof databaseVersionStartSchema>;
export type RecoveryResource = z.infer<typeof recoveryResourceSchema>;
export type RecoveryResourceKind = z.infer<typeof recoveryResourceKindSchema>;
export type RecoveryStatus = z.infer<typeof recoveryStatusSchema>;

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit
) => Promise<Response>;

export class APIError extends Error {
  readonly code: string;
  readonly domain?: ServiceDomain;

  constructor(code: string, message: string, domain?: ServiceDomain) {
    super(message);
    this.name = "APIError";
    this.code = code;
    this.domain = domain;
  }
}

const apiError = async (response: Response, fallback: string) => {
  const parsed = apiErrorSchema.safeParse(
    await response.json().catch(() => null)
  );
  return parsed.success
    ? new APIError(
        parsed.data.error.code,
        parsed.data.error.message,
        parsed.data.error.domain
      )
    : new Error(fallback);
};

export const fetchMeta = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Meta> => {
  const response = await fetcher("/api/v1/meta", {
    headers: { Accept: "application/json" },
    signal,
  });

  if (!response.ok) {
    throw new Error(`meta request failed with ${response.status}`);
  }

  return metaSchema.parse(await response.json());
};

export const fetchIdentity = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Identity> => {
  const response = await fetcher("/api/v1/me", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `identity request failed with ${response.status}`
    );
  }
  return identitySchema.parse(await response.json());
};

export const fetchProjects = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Project[]> => {
  const response = await fetcher("/api/v1/projects", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `projects request failed with ${response.status}`
    );
  }
  return projectsSchema.parse(await response.json());
};

export const createProject = async (
  name: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<Project> => {
  const response = await fetcher("/api/v1/projects", {
    body: JSON.stringify({ name }),
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `project creation failed with ${response.status}`
    );
  }
  return projectSchema.parse(await response.json());
};

export const fetchProjectCanvas = async (
  projectID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ProjectCanvas> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/canvas`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `project canvas request failed with ${response.status}`
    );
  }
  return projectCanvasSchema.parse(await response.json());
};

export const fetchImageCredentials = async (
  projectID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ImageCredential[]> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/image-credentials`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `image credentials request failed with ${response.status}`
    );
  }
  return imageCredentialsSchema.parse(await response.json());
};

export const createImageCredential = async (
  projectID: string,
  input: {
    name: string;
    password: string;
    registryHost: string;
    username: string;
  },
  fetcher: Fetcher = globalThis.fetch
): Promise<ImageCredential> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/image-credentials`,
    {
      body: JSON.stringify(input),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `image credential creation failed with ${response.status}`
    );
  }
  return imageCredentialSchema.parse(await response.json());
};

export const createService = async (
  projectID: string,
  input: CreateServiceInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<Service> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services`,
    {
      body: JSON.stringify(input),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `service creation failed with ${response.status}`
    );
  }
  return serviceSchema.parse(await response.json());
};

export const fetchService = async (
  projectID: string,
  serviceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Service> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `service request failed with ${response.status}`
    );
  }
  return serviceSchema.parse(await response.json());
};

export const updateService = async (
  projectID: string,
  serviceID: string,
  input: UpdateServiceInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<Service> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}`,
    {
      body: JSON.stringify(input),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "PUT",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `service update failed with ${response.status}`
    );
  }
  return serviceSchema.parse(await response.json());
};

const volumePath = (projectID: string, serviceID: string) =>
  `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/volumes`;

export const fetchVolumes = async (
  projectID: string,
  serviceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Volume[]> => {
  const response = await fetcher(volumePath(projectID, serviceID), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `volume request failed with ${response.status}`
    );
  }
  return z.array(volumeSchema).parse(await response.json());
};

export const fetchVolumeOwnerSuggestion = async (
  projectID: string,
  serviceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<VolumeOwnerSuggestion> => {
  const response = await fetcher(
    `${volumePath(projectID, serviceID)}/owner-suggestion`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `volume owner suggestion failed with ${response.status}`
    );
  }
  return volumeOwnerSuggestionSchema.parse(await response.json());
};

export const createVolume = async (
  projectID: string,
  serviceID: string,
  input: CreateVolumeInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<Volume> => {
  const response = await fetcher(volumePath(projectID, serviceID), {
    body: JSON.stringify(input),
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `volume creation failed with ${response.status}`
    );
  }
  return volumeSchema.parse(await response.json());
};

export const deleteVolume = async (
  projectID: string,
  serviceID: string,
  volumeID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `${volumePath(projectID, serviceID)}/${encodeURIComponent(volumeID)}`,
    { headers: { Accept: "application/json" }, method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `volume deletion failed with ${response.status}`
    );
  }
};

const serviceAction = async (
  projectID: string,
  serviceID: string,
  action: "redeploy" | "rollback",
  body: Record<string, number | string>,
  fetcher: Fetcher
): Promise<Service> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/${action}`,
    {
      body: JSON.stringify(body),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `service ${action} failed with ${response.status}`
    );
  }
  return serviceSchema.parse(await response.json());
};

export const redeployService = (
  projectID: string,
  serviceID: string,
  expectedUpdatedAt: number,
  fetcher: Fetcher = globalThis.fetch
): Promise<Service> =>
  serviceAction(
    projectID,
    serviceID,
    "redeploy",
    { expectedUpdatedAt },
    fetcher
  );

export const rollbackService = (
  projectID: string,
  serviceID: string,
  deploymentID: string,
  expectedUpdatedAt: number,
  fetcher: Fetcher = globalThis.fetch
): Promise<Service> =>
  serviceAction(
    projectID,
    serviceID,
    "rollback",
    { deploymentId: deploymentID, expectedUpdatedAt },
    fetcher
  );

export const fetchServiceDeployments = async (
  projectID: string,
  serviceID: string,
  cursor?: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<DeploymentPage> => {
  const query = new URLSearchParams({ limit: "50" });
  if (cursor) {
    query.set("cursor", cursor);
  }
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/deployments?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `deployments request failed with ${response.status}`
    );
  }
  return deploymentPageSchema.parse(await response.json());
};

export const fetchServiceLogs = async (
  projectID: string,
  serviceID: string,
  filters: { contains?: string; deploymentId?: string; limit?: number } = {},
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<LogWindow> => {
  const query = new URLSearchParams({ limit: String(filters.limit ?? 500) });
  if (filters.deploymentId) {
    query.set("deploymentId", filters.deploymentId);
  }
  if (filters.contains) {
    query.set("contains", filters.contains);
  }
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/logs?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `service logs request failed with ${response.status}`
    );
  }
  return logWindowSchema.parse(await response.json());
};

export const fetchServiceTerminalShells = async (
  projectID: string,
  serviceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<string[]> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/terminal/shells`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `terminal shell request failed with ${response.status}`
    );
  }
  return terminalShellsSchema.parse(await response.json()).shells;
};

export const fetchDiskPressure = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<DiskPressure> => {
  const response = await fetcher("/api/v1/infrastructure/disk-pressure", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `disk pressure request failed with ${response.status}`
    );
  }
  return diskPressureSchema.parse(await response.json());
};

export const applySelfUpdate = async (
  fetcher: Fetcher = globalThis.fetch
): Promise<SelfUpdateResult> => {
  const response = await fetcher("/api/v1/infrastructure/update", {
    headers: { Accept: "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `platform update failed with ${response.status}`
    );
  }
  return selfUpdateResultSchema.parse(await response.json());
};

export const fetchAuditEvents = async (
  filters: {
    action?: string;
    actorKind?: AuditEvent["actorKind"];
    cursor?: string;
    limit?: number;
    result?: AuditEvent["result"];
  } = {},
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<AuditPage> => {
  const query = new URLSearchParams({ limit: String(filters.limit ?? 50) });
  if (filters.action) {
    query.set("action", filters.action);
  }
  if (filters.actorKind) {
    query.set("actorKind", filters.actorKind);
  }
  if (filters.cursor) {
    query.set("cursor", filters.cursor);
  }
  if (filters.result) {
    query.set("result", filters.result);
  }
  const response = await fetcher(`/api/v1/audit?${query.toString()}`, {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `audit history request failed with ${response.status}`
    );
  }
  return auditPageSchema.parse(await response.json());
};

export const fetchManagedImageTags = async (
  engine: ManagedImageEngine,
  options: { page?: number; pageSize?: number; search?: string } = {},
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedImagePage> => {
  const query = new URLSearchParams({
    page: String(options.page ?? 1),
    pageSize: String(options.pageSize ?? 50),
  });
  if (options.search) {
    query.set("search", options.search);
  }
  const response = await fetcher(
    `/api/v1/managed-images/${engine}/tags?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed image tags request failed with ${response.status}`
    );
  }
  return managedImagePageSchema.parse(await response.json());
};

const managedRedisPath = (projectID: string, redisID?: string) =>
  `/api/v1/projects/${encodeURIComponent(projectID)}/redis${
    redisID ? `/${encodeURIComponent(redisID)}` : ""
  }`;

export const createManagedRedis = async (
  projectID: string,
  input: CreateManagedRedisInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedRedis> => {
  const response = await fetcher(managedRedisPath(projectID), {
    body: JSON.stringify(input),
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `managed Redis creation failed with ${response.status}`
    );
  }
  return managedRedisSchema.parse(await response.json());
};

export const fetchManagedRedis = async (
  projectID: string,
  redisID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedRedis> => {
  const response = await fetcher(managedRedisPath(projectID, redisID), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `managed Redis request failed with ${response.status}`
    );
  }
  return managedRedisSchema.parse(await response.json());
};

export const scanManagedRedisKeys = async (
  projectID: string,
  redisID: string,
  options: { count?: number; cursor?: string; match?: string } = {},
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RedisKeyPage> => {
  const query = new URLSearchParams({
    count: String(options.count ?? 50),
    cursor: options.cursor ?? "0",
  });
  if (options.match) {
    query.set("match", options.match);
  }
  const response = await fetcher(
    `${managedRedisPath(projectID, redisID)}/keys?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed Redis key scan failed with ${response.status}`
    );
  }
  return redisKeyPageSchema.parse(await response.json());
};

export const previewManagedRedisKey = async (
  projectID: string,
  redisID: string,
  keyBase64: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RedisPreview> => {
  const query = new URLSearchParams({ count: "20", key: keyBase64 });
  const response = await fetcher(
    `${managedRedisPath(projectID, redisID)}/preview?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed Redis value preview failed with ${response.status}`
    );
  }
  return redisPreviewSchema.parse(await response.json());
};

export const mutateManagedRedis = async (
  projectID: string,
  redisID: string,
  input: RedisMutationInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<RedisMutationResult> => {
  const response = await fetcher(
    `${managedRedisPath(projectID, redisID)}/data/mutations`,
    {
      body: JSON.stringify(input),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed Redis mutation failed with ${response.status}`
    );
  }
  return redisMutationResultSchema.parse(await response.json());
};

const managedPostgresPath = (projectID: string, postgresID?: string) =>
  `/api/v1/projects/${encodeURIComponent(projectID)}/postgres${
    postgresID ? `/${encodeURIComponent(postgresID)}` : ""
  }`;

export const createManagedPostgres = async (
  projectID: string,
  input: CreateManagedPostgresInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedPostgres> => {
  const response = await fetcher(managedPostgresPath(projectID), {
    body: JSON.stringify(input),
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `managed PostgreSQL creation failed with ${response.status}`
    );
  }
  return managedPostgresSchema.parse(await response.json());
};

export const fetchManagedPostgres = async (
  projectID: string,
  postgresID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedPostgres> => {
  const response = await fetcher(managedPostgresPath(projectID, postgresID), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `managed PostgreSQL request failed with ${response.status}`
    );
  }
  return managedPostgresSchema.parse(await response.json());
};

export const queryManagedPostgres = async (
  projectID: string,
  postgresID: string,
  sql: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<PostgresQueryResult> => {
  const response = await fetcher(
    `${managedPostgresPath(projectID, postgresID)}/query`,
    {
      body: JSON.stringify({ sql }),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed PostgreSQL query failed with ${response.status}`
    );
  }
  return postgresQueryResultSchema.parse(await response.json());
};

const databaseVersionPath = (
  engine: ManagedImageEngine,
  projectID: string,
  resourceID: string
) => {
  const collection = engine === "postgres" ? "postgres" : "redis";
  return `/api/v1/projects/${encodeURIComponent(projectID)}/${collection}/${encodeURIComponent(resourceID)}/version-change`;
};

export const previewDatabaseVersion = async (
  engine: ManagedImageEngine,
  projectID: string,
  resourceID: string,
  imageTag: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<DatabaseVersionPreview> => {
  const response = await fetcher(
    `${databaseVersionPath(engine, projectID, resourceID)}/preview`,
    {
      body: JSON.stringify({ imageTag }),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Managed database version preview failed with ${response.status}`
    );
  }
  return databaseVersionPreviewSchema.parse(await response.json());
};

export const startDatabaseVersionChange = async (
  engine: ManagedImageEngine,
  projectID: string,
  resourceID: string,
  imageTag: string,
  expectedTargetDigest: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<DatabaseVersionStart> => {
  const response = await fetcher(
    databaseVersionPath(engine, projectID, resourceID),
    {
      body: JSON.stringify({ expectedTargetDigest, imageTag }),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Managed database version change failed with ${response.status}`
    );
  }
  return databaseVersionStartSchema.parse(await response.json());
};

export const fetchDatabaseVersionOperation = async (
  engine: ManagedImageEngine,
  projectID: string,
  resourceID: string,
  operationID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Operation> => {
  const response = await fetcher(
    `${databaseVersionPath(engine, projectID, resourceID)}/${encodeURIComponent(operationID)}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Managed database version operation failed with ${response.status}`
    );
  }
  return operationSchema.parse(await response.json());
};

const objectStorePath = (projectID: string, storeID?: string) =>
  `/api/v1/projects/${encodeURIComponent(projectID)}/object-stores${
    storeID ? `/${encodeURIComponent(storeID)}` : ""
  }`;

export const createObjectStore = async (
  projectID: string,
  input: CreateObjectStoreInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<ObjectStore> => {
  const response = await fetcher(objectStorePath(projectID), {
    body: JSON.stringify(input),
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `object store creation failed with ${response.status}`
    );
  }
  return objectStoreSchema.parse(await response.json());
};

export const fetchObjectStore = async (
  projectID: string,
  storeID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ObjectStore> => {
  const response = await fetcher(objectStorePath(projectID, storeID), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `object store request failed with ${response.status}`
    );
  }
  return objectStoreSchema.parse(await response.json());
};

export const fetchObjects = async (
  projectID: string,
  storeID: string,
  options: { continuationToken?: string; limit?: number; prefix?: string } = {},
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ObjectPage> => {
  const query = new URLSearchParams({ limit: String(options.limit ?? 100) });
  if (options.prefix) {
    query.set("prefix", options.prefix);
  }
  if (options.continuationToken) {
    query.set("continuationToken", options.continuationToken);
  }
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/objects?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `object list failed with ${response.status}`
    );
  }
  return objectPageSchema.parse(await response.json());
};

export const previewObject = async (
  projectID: string,
  storeID: string,
  key: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ObjectPreview> => {
  const query = new URLSearchParams({ key });
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/objects/preview?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `object preview failed with ${response.status}`
    );
  }
  return objectPreviewSchema.parse(await response.json());
};

export const objectDownloadURL = (
  projectID: string,
  storeID: string,
  key: string
) =>
  `${objectStorePath(projectID, storeID)}/objects/download?${new URLSearchParams({ key }).toString()}`;

export const uploadObject = async (
  projectID: string,
  storeID: string,
  key: string,
  file: Blob,
  fetcher: Fetcher = globalThis.fetch
): Promise<ObjectMetadata> => {
  const query = new URLSearchParams({ key });
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/objects?${query.toString()}`,
    {
      body: file,
      headers: {
        Accept: "application/json",
        "Content-Type": file.type || "application/octet-stream",
      },
      method: "PUT",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `object upload failed with ${response.status}`
    );
  }
  return objectMetadataSchema.parse(await response.json());
};

export const deleteObject = async (
  projectID: string,
  storeID: string,
  key: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const query = new URLSearchParams({ key });
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/objects?${query.toString()}`,
    { method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `object deletion failed with ${response.status}`
    );
  }
};

const registryRepositoryPath = (repositoryID?: string) =>
  `/api/v1/registry/repositories${repositoryID ? `/${encodeURIComponent(repositoryID)}` : ""}`;

export const fetchBackupTarget = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupTarget> => {
  const response = await fetcher("/api/v1/backups/target", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup target request failed with ${response.status}`
    );
  }
  return backupTargetSchema.parse(await response.json());
};

export const setBackupTarget = async (
  input: SetBackupTargetInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupTarget> => {
  const response = await fetcher("/api/v1/backups/target", {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "PUT",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup target update failed with ${response.status}`
    );
  }
  return backupTargetSchema.parse(await response.json());
};

export const deleteBackupTarget = async (
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher("/api/v1/backups/target", {
    method: "DELETE",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup target deletion failed with ${response.status}`
    );
  }
};

const backupResourcePath = (kind: RecoveryResourceKind, resourceID: string) =>
  `/api/v1/backups/resources/${encodeURIComponent(kind)}/${encodeURIComponent(resourceID)}`;

export const fetchBackupPolicies = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupPolicy[]> => {
  const response = await fetcher("/api/v1/backups/resources", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup policy request failed with ${response.status}`
    );
  }
  return backupPoliciesSchema.parse(await response.json()).policies;
};

export const setBackupPolicy = async (
  kind: RecoveryResourceKind,
  resourceID: string,
  input: { cron: string; enabled: boolean; retentionCount: number },
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupPolicy> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/policy`,
    {
      body: JSON.stringify(input),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "PUT",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup policy update failed with ${response.status}`
    );
  }
  return backupPolicySchema.parse(await response.json());
};

export const runBackupNow = async (
  kind: RecoveryResourceKind,
  resourceID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupRecord> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/run`,
    {
      headers: { Accept: "application/json" },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup request failed with ${response.status}`
    );
  }
  return backupRecordSchema.parse(await response.json());
};

export const fetchBackupHistory = async (
  kind: RecoveryResourceKind,
  resourceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupRecord[]> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/history?limit=50`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup history request failed with ${response.status}`
    );
  }
  return backupHistorySchema.parse(await response.json()).backups;
};

export const fetchBackupGenerations = async (
  kind: RecoveryResourceKind,
  resourceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupGeneration[]> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/generations`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup generation request failed with ${response.status}`
    );
  }
  return backupGenerationsSchema.parse(await response.json()).generations;
};

export const restoreBackupGeneration = async (
  kind: RecoveryResourceKind,
  resourceID: string,
  generationID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<Operation> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/restore`,
    {
      body: JSON.stringify({
        destructiveConfirmed: true,
        generationId: generationID,
        mode: "replace",
      }),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup restore request failed with ${response.status}`
    );
  }
  return operationSchema.parse(await response.json());
};

export const fetchOperation = async (
  operationID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Operation> => {
  const response = await fetcher(
    `/api/v1/operations/${encodeURIComponent(operationID)}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Operation request failed with ${response.status}`
    );
  }
  return operationSchema.parse(await response.json());
};

export const fetchRecoveryStatus = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RecoveryStatus> => {
  const response = await fetcher("/api/v1/recovery", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Recovery status request failed with ${response.status}`
    );
  }
  return recoveryStatusSchema.parse(await response.json());
};

export const retryRecovery = async (
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher("/api/v1/recovery/retry", {
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Recovery retry failed with ${response.status}`
    );
  }
};

export const fetchRegistrySettings = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistrySettings> => {
  const response = await fetcher("/api/v1/registry", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry settings request failed with ${response.status}`
    );
  }
  return registrySettingsSchema.parse(await response.json());
};

export const setRegistryHostname = async (
  hostname: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistrySettings> => {
  const response = await fetcher("/api/v1/registry/hostname", {
    body: JSON.stringify({ hostname }),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "PUT",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry hostname update failed with ${response.status}`
    );
  }
  return registrySettingsSchema.parse(await response.json());
};

export const fetchRegistryRepositories = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistryRepository[]> => {
  const response = await fetcher(registryRepositoryPath(), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry repositories request failed with ${response.status}`
    );
  }
  return registryRepositoriesSchema.parse(await response.json()).repositories;
};

export const createRegistryRepository = async (
  input: CreateRegistryRepositoryInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistryRepository> => {
  const response = await fetcher(registryRepositoryPath(), {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry repository creation failed with ${response.status}`
    );
  }
  return registryRepositorySchema.parse(await response.json());
};

export const setRegistryRepositoryPublicPull = async (
  repositoryID: string,
  publicPull: boolean,
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistryRepository> => {
  const response = await fetcher(
    `${registryRepositoryPath(repositoryID)}/public-pull`,
    {
      body: JSON.stringify({ publicPull }),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "PUT",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry public pull update failed with ${response.status}`
    );
  }
  return registryRepositorySchema.parse(await response.json());
};

export const fetchRegistryImages = async (
  repositoryID: string,
  options: { after?: string; limit?: number } = {},
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
) => {
  const query = new URLSearchParams({ limit: String(options.limit ?? 100) });
  if (options.after) {
    query.set("after", options.after);
  }
  const response = await fetcher(
    `${registryRepositoryPath(repositoryID)}/images?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry images request failed with ${response.status}`
    );
  }
  return registryImagesSchema.parse(await response.json());
};

export const fetchRegistryImage = async (
  repositoryID: string,
  digest: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistryImage> => {
  const response = await fetcher(
    `${registryRepositoryPath(repositoryID)}/images/${encodeURIComponent(digest)}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry image request failed with ${response.status}`
    );
  }
  return registryImageSchema.parse(await response.json());
};

export const deleteRegistryTag = async (
  repositoryID: string,
  tag: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `${registryRepositoryPath(repositoryID)}/tags/${encodeURIComponent(tag)}`,
    { method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry tag deletion failed with ${response.status}`
    );
  }
};

export const deleteRegistryImage = async (
  repositoryID: string,
  digest: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `${registryRepositoryPath(repositoryID)}/manifests/${encodeURIComponent(digest)}`,
    { method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry image deletion failed with ${response.status}`
    );
  }
};

export const deleteRegistryRepository = async (
  repositoryID: string,
  expectedName: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(registryRepositoryPath(repositoryID), {
    body: JSON.stringify({ expectedName }),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "DELETE",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry repository deletion failed with ${response.status}`
    );
  }
};

const registryCredentialsPath = (repositoryID: string, credentialID?: string) =>
  `${registryRepositoryPath(repositoryID)}/credentials${credentialID ? `/${encodeURIComponent(credentialID)}` : ""}`;

export const fetchRegistryCredentials = async (
  repositoryID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistryCredential[]> => {
  const response = await fetcher(registryCredentialsPath(repositoryID), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry credentials request failed with ${response.status}`
    );
  }
  return registryCredentialsSchema.parse(await response.json()).credentials;
};

export const createRegistryCredential = async (
  repositoryID: string,
  input: { name: string; permission: "pull" | "pull_push" },
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistryCredential> => {
  const response = await fetcher(registryCredentialsPath(repositoryID), {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry credential creation failed with ${response.status}`
    );
  }
  return registryCredentialSchema.parse(await response.json());
};

export const deleteRegistryCredential = async (
  repositoryID: string,
  credentialID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    registryCredentialsPath(repositoryID, credentialID),
    { method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry credential deletion failed with ${response.status}`
    );
  }
};

export const cleanupRegistryRepository = async (
  repositoryID: string,
  dryRun: boolean,
  fetcher: Fetcher = globalThis.fetch
): Promise<RegistryCleanup> => {
  const response = await fetcher(
    `${registryRepositoryPath(repositoryID)}/cleanup`,
    {
      body: JSON.stringify({ dryRun }),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Registry cleanup failed with ${response.status}`
    );
  }
  return registryCleanupSchema.parse(await response.json());
};

const serviceDomainsPath = (projectID: string, serviceID: string) =>
  `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/domains`;

export const fetchServiceDomains = async (
  projectID: string,
  serviceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ServiceDomain[]> => {
  const response = await fetcher(serviceDomainsPath(projectID, serviceID), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `service domains request failed with ${response.status}`
    );
  }
  return serviceDomainsSchema.parse(await response.json()).domains;
};

export const attachServiceDomain = async (
  projectID: string,
  serviceID: string,
  hostname: string,
  move = false,
  fetcher: Fetcher = globalThis.fetch
): Promise<ServiceDomain> => {
  const response = await fetcher(serviceDomainsPath(projectID, serviceID), {
    body: JSON.stringify({ hostname, move }),
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `domain attachment failed with ${response.status}`
    );
  }
  return serviceDomainSchema.parse(await response.json());
};

export const detachServiceDomain = async (
  projectID: string,
  serviceID: string,
  hostname: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `${serviceDomainsPath(projectID, serviceID)}/${encodeURIComponent(hostname)}`,
    { method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `domain removal failed with ${response.status}`
    );
  }
};

export const fetchAPITokens = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<APIToken[]> => {
  const response = await fetcher("/api/v1/tokens", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `API tokens request failed with ${response.status}`
    );
  }
  return apiTokensSchema.parse(await response.json()).tokens;
};

export const createAPIToken = async (
  input: { name: string; projectId?: string; role: APIToken["role"] },
  fetcher: Fetcher = globalThis.fetch
): Promise<APIToken> => {
  const response = await fetcher("/api/v1/tokens", {
    body: JSON.stringify(input),
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `API token creation failed with ${response.status}`
    );
  }
  const token = apiTokenSchema.parse(await response.json());
  if (!token.token) {
    throw new Error("API token creation response omitted the one-time secret");
  }
  return token;
};

export const revokeAPIToken = async (
  tokenID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `/api/v1/tokens/${encodeURIComponent(tokenID)}`,
    { method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `API token revoke failed with ${response.status}`
    );
  }
};
