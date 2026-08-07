import { z } from "zod";

const metaSchema = z.object({
  architecture: z.string(),
  os: z.string(),
  status: z.enum(["bootstrapping", "ready", "recovery"]),
  version: z.string(),
});

export type Meta = z.infer<typeof metaSchema>;

const identitySchema = z.object({
  avatarUrl: z.url().optional(),
  email: z.email(),
  name: z.string().trim().min(1).optional(),
  subject: z.string().min(1),
});

const accessIdentityProfileSchema = z.object({
  avatar_url: z.string().nullish(),
  custom: z.record(z.string(), z.unknown()).nullish(),
  idp: z.record(z.string(), z.unknown()).nullish(),
  name: z.string().nullish(),
  oidc_fields: z.record(z.string(), z.unknown()).nullish(),
  picture: z.string().nullish(),
});

export type Identity = z.infer<typeof identitySchema>;

const profileString = (value: unknown): string | undefined =>
  typeof value === "string" ? value : undefined;

const identityDisplayName = (
  profile: z.infer<typeof accessIdentityProfileSchema>
) => {
  const name =
    profile.name?.trim() ||
    profileString(profile.custom?.name)?.trim() ||
    profileString(profile.oidc_fields?.name)?.trim();
  return name || undefined;
};

const identityAvatarURL = (
  profile: z.infer<typeof accessIdentityProfileSchema>
) => {
  const candidates = [
    profile.avatar_url,
    profile.picture,
    profileString(profile.custom?.avatar_url),
    profileString(profile.custom?.picture),
    profileString(profile.oidc_fields?.avatar_url),
    profileString(profile.oidc_fields?.picture),
    profileString(profile.idp?.avatar_url),
    profileString(profile.idp?.picture),
  ];
  for (const candidate of candidates) {
    if (!candidate) {
      continue;
    }
    try {
      const url = new URL(candidate);
      if (url.protocol === "https:") {
        return url.toString();
      }
    } catch {
      // An IdP profile is optional display data; malformed URLs are ignored.
    }
  }
};

const projectSchema = z.object({
  createdAt: z.number().int().nonnegative(),
  hasIcon: z.boolean(),
  id: z.string().min(1),
  name: z.string().min(1),
  networkGatewayCount: z.number().int().nonnegative(),
  objectStoreCount: z.number().int().nonnegative(),
  postgresCount: z.number().int().nonnegative(),
  redisCount: z.number().int().nonnegative(),
  serviceCount: z.number().int().nonnegative(),
  updatedAt: z.number().int().nonnegative(),
});

const projectsSchema = z.array(projectSchema);
const projectWebhookEventTypeSchema = z.enum([
  "deployment.started",
  "deployment.succeeded",
  "deployment.failed",
  "deployment.interrupted",
  "deployment.skipped",
]);
const projectWebhookSchema = z.object({
  createdAt: z.number().int().nonnegative(),
  eventTypes: z.array(projectWebhookEventTypeSchema).min(1),
  id: z.string().min(1),
  projectId: z.string().min(1),
  updatedAt: z.number().int().nonnegative(),
  url: z.url(),
});
const projectWebhooksSchema = z.object({
  eventTypes: z.array(projectWebhookEventTypeSchema),
  webhooks: z.array(projectWebhookSchema),
});
const serviceDomainSchema = z.object({
  createdAt: z.number().int().positive(),
  hostname: z.string().min(1),
  internalOutputName: z.string().min(1),
  projectId: z.string().min(1).optional(),
  projectName: z.string().min(1).optional(),
  publicOutputName: z.string().min(1),
  serviceId: z.string().min(1),
  serviceName: z.string().min(1).optional(),
  targetPort: z.number().int().min(1).max(65_535),
});
const serviceDomainsSchema = z.object({
  domains: z.array(serviceDomainSchema),
});
const serviceDomainDNSStatusSchema = z.object({
  status: z.enum(["unmanaged", "pending", "ready"]),
});
const serviceListenerSchema = z.object({
  createdAt: z.number().int().positive(),
  projectId: z.string().min(1).optional(),
  projectName: z.string().min(1).optional(),
  protocol: z.enum(["tcp", "udp"]),
  publicPort: z.number().int().min(1).max(65_535),
  serviceId: z.string().min(1),
  serviceName: z.string().min(1).optional(),
  targetPort: z.number().int().min(1).max(65_535),
});
const serviceListenersSchema = z.object({
  listeners: z.array(serviceListenerSchema),
});
const containerPortSchema = z.object({
  port: z.number().int().min(1).max(65_535),
  protocol: z.enum(["tcp", "udp"]),
});
const containerPortsSchema = z.object({
  ports: z.array(containerPortSchema),
});

const hostNetworkAddressSchema = z.object({
  address: z.string().min(1),
  interface: z.string().min(1),
});

const networkGatewayInputSchema = z.object({
  interfaceName: z.string(),
  listenPort: z.number().int().min(1).max(65_535),
  mode: z.enum(["import", "export"]),
  name: z.string().min(1),
  protocol: z.enum(["tcp", "udp"]),
  remoteHost: z.string().default(""),
  remotePort: z.number().int().min(0).max(65_535),
  sourceAddress: z.string(),
  targetPort: z.number().int().min(0).max(65_535),
  targetServiceId: z.string().default(""),
  transport: z.enum(["vpc", "mesh"]),
});

const networkGatewaySchema = networkGatewayInputSchema.extend({
  createdAt: z.number().int().positive(),
  id: z.string().min(1),
  internalHostname: z.string().min(1).optional(),
  projectId: z.string().min(1),
  projectName: z.string().min(1),
  targetService: z.string().optional(),
  updatedAt: z.number().int().positive(),
});

export type HostNetworkAddress = z.infer<typeof hostNetworkAddressSchema>;
export type NetworkGatewayInput = z.infer<typeof networkGatewayInputSchema>;
export type NetworkGateway = z.infer<typeof networkGatewaySchema>;
const apiErrorSchema = z.object({
  error: z.object({
    code: z.string(),
    domain: serviceDomainSchema.optional(),
    listener: serviceListenerSchema.optional(),
    message: z.string(),
  }),
});

export type Project = z.infer<typeof projectSchema>;
export type ProjectWebhook = z.infer<typeof projectWebhookSchema>;
export type ProjectWebhookEventType = z.infer<
  typeof projectWebhookEventTypeSchema
>;
export type ServiceDomain = z.infer<typeof serviceDomainSchema>;
export type ServiceDomainDNSStatus = z.infer<
  typeof serviceDomainDNSStatusSchema
>["status"];
export type ServiceListener = z.infer<typeof serviceListenerSchema>;
export type ContainerPort = z.infer<typeof containerPortSchema>;

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

const imageSourceSchema = z.discriminatedUnion("type", [
  z.object({
    autoUpdate: z.boolean().optional().default(false),
    image: z.object({ reference: z.string().min(1) }),
    minimumReleaseAgeDays: z.number().int().positive().max(36_500).optional(),
    type: z.literal("public_image"),
  }),
  z.object({
    autoUpdate: z.boolean().optional().default(false),
    image: z.object({ reference: z.string().min(1) }),
    minimumReleaseAgeDays: z.number().int().positive().max(36_500).optional(),
    type: z.literal("private_image"),
  }),
]);

const dockerImageUploadSourceSchema = z.object({
  dockerUpload: z.object({
    branch: z.string().min(1),
    previewDomain: z.string().optional(),
    previews: z.boolean(),
    repository: z.string().min(1),
    workflows: z.array(z.string().min(1)),
  }),
  type: z.literal("docker_image_upload"),
});

const serviceSourceSchema = z.union([
  imageSourceSchema,
  dockerImageUploadSourceSchema,
  z.object({ type: z.literal("unconfigured") }),
]);
export type ServiceSource = z.infer<typeof serviceSourceSchema>;

const canvasResourceSchema = z.object({
  activeDeploymentId: z.string().min(1).optional(),
  bucketName: z.string().optional(),
  enabled: z.boolean(),
  gatewayListenPort: z.number().int().min(1).max(65_535).optional(),
  gatewayMode: z.enum(["import", "export"]).optional(),
  gatewayProtocol: z.enum(["tcp", "udp"]).optional(),
  gatewayRemoteHost: z.string().optional(),
  gatewayRemotePort: z.number().int().min(1).max(65_535).optional(),
  gatewaySourceAddress: z.string().optional(),
  gatewayTargetPort: z.number().int().min(1).max(65_535).optional(),
  gatewayTargetServiceId: z.string().optional(),
  gatewayTransport: z.enum(["vpc", "mesh"]).optional(),
  id: z.string().min(1),
  imageDigest: z.string().min(1).optional(),
  imageReference: z.string().min(1).optional(),
  internalHostname: z.string().min(1),
  kind: z.enum([
    "service",
    "postgres",
    "redis",
    "object_store",
    "network_gateway",
  ]),
  name: z.string().min(1),
  source: serviceSourceSchema.optional(),
  status: z.enum(["degraded", "disabled", "failed", "pending", "running"]),
  statusMessage: z.string().optional(),
  volumes: z.array(
    z.object({
      containerPath: z.string().min(1).optional(),
      id: z.string().min(1),
      name: z.string().min(1),
    })
  ),
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

const serviceRegistryCredentialSchema = z.object({
  password: z.string().min(1),
  registryHost: z.string().min(1),
  username: z.string().min(1),
});
export type ServiceRegistryCredential = z.infer<
  typeof serviceRegistryCredentialSchema
>;

const healthCheckSchema = z.object({
  path: z.string().min(1),
  port: z.number().int().min(1).max(65_535),
  timeoutSeconds: z.number().int().min(1).max(3600),
});

const beforeDeploySchema = z.object({
  cloudflareHostnames: z.array(z.string().min(1)),
  command: z.string().min(1).optional(),
});

export type BeforeDeploy = z.infer<typeof beforeDeploySchema>;

const portForwardSchema = z.object({
  repository: z.string().min(1),
  workflows: z.array(z.string().min(1)),
});

export type PortForwardAccess = z.infer<typeof portForwardSchema>;

const serviceSchema = z.object({
  activeConfigHash: z.string().min(1).optional(),
  activeDeploymentId: z.string().min(1).optional(),
  activeImageDigest: z.string().min(1).optional(),
  args: z.array(z.string()).optional(),
  beforeDeploy: beforeDeploySchema.optional(),
  command: z.array(z.string()).optional(),
  cpuMillicores: z.number().int().nonnegative().optional(),
  createdAt: z.number().int().positive(),
  enabled: z.boolean(),
  environment: z.record(z.string(), z.string()),
  healthCheck: healthCheckSchema.optional(),
  id: z.string().min(1),
  memoryMaxBytes: z.number().int().nonnegative().optional(),
  name: z.string().min(1),
  portForward: portForwardSchema.optional(),
  projectId: z.string().min(1),
  registryCredential: serviceRegistryCredentialSchema.optional(),
  secretReferences: z.array(
    z.object({
      environmentName: z.string().min(1),
      secretId: z.string().min(1),
    })
  ),
  source: serviceSourceSchema,
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
  projectId: z.string().min(1),
  serviceId: z.string().min(1),
});

export type Volume = z.infer<typeof volumeSchema>;

export interface CreateVolumeInput {
  name: string;
}

export interface CreateServiceVolumeInput extends CreateVolumeInput {
  containerPath?: string;
}

export interface CreateServiceInput {
  beforeDeploy?: BeforeDeploy;
  domains?: Pick<ServiceDomain, "hostname" | "targetPort">[];
  environment: Record<string, string>;
  healthCheck?: z.infer<typeof healthCheckSchema>;
  listeners?: Pick<ServiceListener, "protocol" | "publicPort" | "targetPort">[];
  name: string;
  portForward?: PortForwardAccess;
  registryCredential?: Pick<ServiceRegistryCredential, "password" | "username">;
  source: ServiceSource;
  volumes?: CreateServiceVolumeInput[];
}

export interface UpdateServiceInput {
  args?: string[];
  beforeDeploy?: BeforeDeploy;
  command?: string[];
  cpuMillicores?: number;
  enabled: boolean;
  environment: Record<string, string>;
  expectedUpdatedAt: number;
  healthCheck?: z.infer<typeof healthCheckSchema>;
  memoryMaxBytes?: number;
  portForward?: PortForwardAccess;
  registryCredential?: Pick<ServiceRegistryCredential, "password" | "username">;
  secretReferences: Service["secretReferences"];
  source: ServiceSource;
  volumeMounts: Service["volumeMounts"];
}

const deploymentSchema = z.object({
  commitMessage: z.string().min(1).optional(),
  createdAt: z.number().int().positive(),
  errorCode: z.string().optional(),
  errorMessage: z.string().optional(),
  finishedAt: z.number().int().positive().optional(),
  id: z.string().min(1),
  imageDigest: z.string().min(1).optional(),
  imageReference: z.string().min(1).optional(),
  serviceConfigHash: z.string().min(1),
  serviceId: z.string().min(1),
  snapshot: serviceSchema.pick({
    args: true,
    beforeDeploy: true,
    command: true,
    cpuMillicores: true,
    environment: true,
    healthCheck: true,
    memoryMaxBytes: true,
    secretReferences: true,
    source: true,
    volumeMounts: true,
  }),
  sourceRevision: z.string().min(1).optional(),
  status: z.enum([
    "failed",
    "interrupted",
    "running",
    "skipped",
    "succeeded",
    "waiting",
  ]),
});

const deploymentPageSchema = z.object({
  deployments: z.array(deploymentSchema),
  nextCursor: z.string().min(1).optional(),
});

export type Deployment = z.infer<typeof deploymentSchema>;
export type DeploymentPage = z.infer<typeof deploymentPageSchema>;

const previewDeploymentSchema = z.object({
  createdAt: z.number().int().positive(),
  errorMessage: z.string().optional(),
  expiresAt: z.number().int().positive(),
  finishedAt: z.number().int().positive().optional(),
  hostname: z.string().min(1),
  id: z.string().min(1),
  serviceId: z.string().min(1),
  status: z.enum(["active", "deploying", "failed", "interrupted", "stopped"]),
  tag: z.string().min(1),
  targetPort: z.number().int().min(1).max(65_535),
});

export type PreviewDeployment = z.infer<typeof previewDeploymentSchema>;

const runtimeDeploymentSchema = z.object({
  active: z.boolean(),
  createdAt: z.number().int().positive(),
  errorCode: z.string().optional(),
  errorMessage: z.string().optional(),
  finishedAt: z.number().int().positive().optional(),
  id: z.string().min(1),
  imageDigest: z.string().min(1),
  imageTag: z.string().min(1),
  resourceId: z.string().min(1),
  resourceKind: z.enum(["postgres", "redis"]),
  status: z.enum(["failed", "interrupted", "removed", "running", "succeeded"]),
});

const runtimeDeploymentPageSchema = z.object({
  deployments: z.array(runtimeDeploymentSchema),
  nextCursor: z.string().min(1).optional(),
});

export type RuntimeDeployment = z.infer<typeof runtimeDeploymentSchema>;
export type RuntimeDeploymentPage = z.infer<typeof runtimeDeploymentPageSchema>;

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

const buildLogSchema = z.object({ text: z.string() });

export const parseLogStreamMessage = (value: unknown): LogStreamMessage =>
  logStreamMessageSchema.parse(value);

const terminalShellsSchema = z.object({
  shells: z.array(z.enum(["/bin/sh", "/bin/bash"])),
});

const serverTerminalTokenSchema = z.object({
  expiresAt: z.number().int().positive(),
  token: z.string().min(1),
});

export type ServerTerminalToken = z.infer<typeof serverTerminalTokenSchema>;

const diskPressureSchema = z.object({
  availableBytes: z.number().int().nonnegative(),
  availableInodes: z.number().int().nonnegative(),
  byteBasisPoints: z.number().int().min(0).max(10_000),
  checkedAt: z.number().int().positive(),
  components: z.array(
    z.object({
      bytes: z.number().int().nonnegative(),
      id: z.string().min(1),
    })
  ),
  componentsCheckedAt: z.number().int().positive().optional(),
  inodeBasisPoints: z.number().int().min(0).max(10_000),
  level: z.enum(["normal", "low", "critical", "emergency"]),
  reservePresent: z.boolean(),
  totalBytes: z.number().int().positive(),
  totalInodes: z.number().int().nonnegative(),
});

export type DiskPressure = z.infer<typeof diskPressureSchema>;

const imageGarbageCollectionResultSchema = z.object({
  buildCacheImagesRemoved: z.number().int().nonnegative(),
  finalImagesRemoved: z.number().int().nonnegative(),
  orphanLayersRemoved: z.number().int().nonnegative(),
  removedBytes: z.number().int().nonnegative(),
  skipped: z.number().int().nonnegative(),
});

export type ImageGarbageCollectionResult = z.infer<
  typeof imageGarbageCollectionResultSchema
>;

const infrastructureLogRecordSchema = z.object({
  cursor: z.string().min(1),
  identifier: z.string().optional(),
  message: z.string(),
  pid: z.string().optional(),
  priority: z.number().int().min(0).max(7),
  timestamp: z.iso.datetime({ offset: true }),
});

const infrastructureLogWindowSchema = z.object({
  // Cursors are opaque journal positions and must only be passed back to the API.
  nextCursor: z.string().min(1).optional(),
  records: z.array(infrastructureLogRecordSchema),
});

export type InfrastructureLogRecord = z.infer<
  typeof infrastructureLogRecordSchema
>;
export type InfrastructureLogWindow = z.infer<
  typeof infrastructureLogWindowSchema
>;

export interface InfrastructureLogsQuery {
  beforeCursor?: string;
  limit?: number;
}

const proxyUsageSchema = z.object({
  http: z.object({
    activeRequests: z.number().int().nonnegative(),
    activeRequestsPeak: z.number().int().nonnegative(),
    latencyP50Millis: z.number().nonnegative().optional(),
    latencyP95Millis: z.number().nonnegative().optional(),
    latencyP99Millis: z.number().nonnegative().optional(),
    requestsPeakPerSecond: z.number().nonnegative().optional(),
    requestsPerSecond: z.number().nonnegative().optional(),
    requestsTotal: z.number().int().nonnegative(),
    responses2xxPerSecond: z.number().nonnegative().optional(),
    responses3xxPerSecond: z.number().nonnegative().optional(),
    responses4xxPerSecond: z.number().nonnegative().optional(),
    responses5xxPerSecond: z.number().nonnegative().optional(),
  }),
  tcp: z.object({
    activeConnections: z.number().int().nonnegative(),
    activeConnectionsPeak: z.number().int().nonnegative(),
    connectionsPeakPerSecond: z.number().nonnegative().optional(),
    connectionsPerSecond: z.number().nonnegative().optional(),
    connectionsTotal: z.number().int().nonnegative(),
  }),
  udp: z.object({
    egressPacketsPeakPerSecond: z.number().nonnegative().optional(),
    egressPacketsPerSecond: z.number().nonnegative().optional(),
    egressPacketsTotal: z.number().int().nonnegative(),
    ingressPacketsPeakPerSecond: z.number().nonnegative().optional(),
    ingressPacketsPerSecond: z.number().nonnegative().optional(),
    ingressPacketsTotal: z.number().int().nonnegative(),
  }),
});

const hostUsageSchema = z.object({
  cpuCores: z.number().int().positive(),
  cpuMillicores: z.number().int().nonnegative().optional(),
  cpuPeakMillicores: z.number().int().nonnegative().optional(),
  memoryPeakBytes: z.number().int().nonnegative(),
  memoryTotalBytes: z.number().int().positive(),
  memoryUsedBytes: z.number().int().nonnegative(),
  networkEgressBytesPerSecond: z.number().int().nonnegative().optional(),
  networkEgressPeakBytesPerSecond: z.number().int().nonnegative().optional(),
  networkIngressBytesPerSecond: z.number().int().nonnegative().optional(),
  networkIngressPeakBytesPerSecond: z.number().int().nonnegative().optional(),
  networkInterface: z.string().min(1),
  observedAt: z.number().int().positive(),
});

const resourceUsageSchema = z.object({
  // Rates are calculated continuously by the daemon and stay absent whenever
  // the current two-second interval has no valid counter delta.
  cpuMillicores: z.number().int().nonnegative().optional(),
  cpuPeakMillicores: z.number().int().nonnegative().optional(),
  diskBytes: z.number().int().nonnegative().optional(),
  host: hostUsageSchema.optional(),
  hostCpuCores: z.number().int().nonnegative(),
  hostMemoryBytes: z.number().int().nonnegative(),
  memoryBytes: z.number().int().nonnegative(),
  memoryPeakBytes: z.number().int().nonnegative(),
  networkAvailable: z.boolean(),
  networkEgressBytesPerSecond: z.number().int().nonnegative().optional(),
  networkEgressPeakBytesPerSecond: z.number().int().nonnegative().optional(),
  networkIngressBytesPerSecond: z.number().int().nonnegative().optional(),
  networkIngressPeakBytesPerSecond: z.number().int().nonnegative().optional(),
  observedAt: z.number().int().positive(),
  proxy: proxyUsageSchema.optional(),
  running: z.boolean(),
  runningResources: z.number().int().nonnegative(),
  totalResources: z.number().int().nonnegative(),
  trafficRoutes: z.object({
    http: z.boolean(),
    tcp: z.boolean(),
    udp: z.boolean(),
  }),
});

const resourceUsageHistoryPointSchema = z.object({
  cpuMillicores: z.number().int().nonnegative().optional(),
  cpuPeakMillicores: z.number().int().nonnegative().optional(),
  diskBytes: z.number().int().nonnegative().optional(),
  durationMillis: z.number().int().positive(),
  memoryBytes: z.number().int().nonnegative(),
  memoryPeakBytes: z.number().int().nonnegative(),
  networkEgressBytesPerSecond: z.number().int().nonnegative().optional(),
  networkEgressPeakBytesPerSecond: z.number().int().nonnegative().optional(),
  networkIngressBytesPerSecond: z.number().int().nonnegative().optional(),
  networkIngressPeakBytesPerSecond: z.number().int().nonnegative().optional(),
  observedAt: z.number().int().positive(),
  proxy: proxyUsageSchema.optional(),
  running: z.boolean(),
});

const resourceUsageHistorySchema = z.object({
  from: z.number().int().positive(),
  points: z.array(resourceUsageHistoryPointSchema),
  series: z.array(
    z.object({
      id: z.string().min(1),
      kind: z.enum([
        "network_gateway",
        "postgres",
        "project",
        "redis",
        "service",
      ]),
      name: z.string().min(1),
      points: z.array(resourceUsageHistoryPointSchema),
    })
  ),
  stepMillis: z.number().int().positive(),
  to: z.number().int().positive(),
});

export type ResourceUsage = z.infer<typeof resourceUsageSchema>;
export type ResourceUsageHistory = z.infer<typeof resourceUsageHistorySchema>;
export type ResourceUsageKind =
  | "network_gateway"
  | "postgres"
  | "redis"
  | "service";
export type ResourceUsageRange = "1d" | "1h" | "30d" | "6h" | "7d";

const selfUpdateResultSchema = z.object({
  previousVersion: z.string().min(1),
  targetVersion: z.string().min(1),
});

const selfUpdateStatusSchema = z.object({
  currentVersion: z.string().min(1),
  latestVersion: z.string().min(1),
  updateAvailable: z.boolean(),
  updateSupported: z.boolean(),
});

export type SelfUpdateResult = z.infer<typeof selfUpdateResultSchema>;
export type SelfUpdateStatus = z.infer<typeof selfUpdateStatusSchema>;

const auditEventSchema = z.object({
  action: z.string().min(1),
  actorId: z.string().min(1),
  actorKind: z.enum(["access", "token", "system", "local_root"]),
  createdAt: z.number().int().positive(),
  id: z.string().min(1),
  metadata: z.record(z.string(), z.unknown()),
  projectId: z.string().min(1).optional(),
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
  password: z.string().min(1),
  port: z.literal(6379),
  portForward: portForwardSchema.optional(),
  projectId: z.string().min(1),
  updatedAt: z.number().int().positive(),
});

const managedRedisPersistenceSchema = z.object({
  actualRpoMillis: z.number().int().nonnegative(),
  backgroundSaveInProgress: z.boolean(),
  lastBackgroundSaveSuccessful: z.boolean(),
  lastSuccessfulSaveAt: z.number().int().positive(),
  needsAttention: z.boolean(),
  observedAt: z.number().int().positive(),
  targetRpoMillis: z.number().int().positive(),
});

const managedRedisStatsSchema = z.object({
  aofEnabled: z.boolean(),
  blockedClients: z.number().int().nonnegative(),
  commands: z.array(
    z.object({
      calls: z.number().int().nonnegative(),
      microsPerCall: z.number().nonnegative(),
      name: z.string().min(1),
      p50Micros: z.number().nonnegative(),
      p95Micros: z.number().nonnegative(),
      p99Micros: z.number().nonnegative(),
      totalMicros: z.number().int().nonnegative(),
    })
  ),
  connectedClients: z.number().int().nonnegative(),
  evictedKeys: z.number().int().nonnegative(),
  evictionPolicy: z.string().min(1),
  expiredKeys: z.number().int().nonnegative(),
  fragmentationRatio: z.number().nonnegative(),
  keyspaceHits: z.number().int().nonnegative(),
  keyspaceMisses: z.number().int().nonnegative(),
  keyspaces: z.array(
    z.object({
      averageTtlMillis: z.number().int().nonnegative(),
      database: z.string().min(1),
      expires: z.number().int().nonnegative(),
      keys: z.number().int().nonnegative(),
    })
  ),
  latencyP50Micros: z.number().nonnegative(),
  latencyP95Micros: z.number().nonnegative(),
  latencyP99Micros: z.number().nonnegative(),
  maxMemoryBytes: z.number().int().nonnegative(),
  operationsPerSecond: z.number().int().nonnegative(),
  peakMemoryBytes: z.number().int().nonnegative(),
  rejectedConnections: z.number().int().nonnegative(),
  rssMemoryBytes: z.number().int().nonnegative(),
  slowlog: z.array(
    z.object({
      client: z.string(),
      command: z.string(),
      durationMicros: z.number().int().nonnegative(),
      id: z.number().int().nonnegative(),
      timestampMillis: z.number().int().nonnegative(),
    })
  ),
  totalCommands: z.number().int().nonnegative(),
  totalConnections: z.number().int().nonnegative(),
  totalNetInputBytes: z.number().int().nonnegative(),
  totalNetOutputBytes: z.number().int().nonnegative(),
  uptimeSeconds: z.number().int().nonnegative(),
  usedMemoryBytes: z.number().int().nonnegative(),
  version: z.string().min(1),
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
export type ManagedRedisPersistence = z.infer<
  typeof managedRedisPersistenceSchema
>;
export type ManagedRedisStats = z.infer<typeof managedRedisStatsSchema>;
export type RedisKey = z.infer<typeof redisKeySchema>;
export type RedisKeyPage = z.infer<typeof redisKeyPageSchema>;
export type RedisPreview = z.infer<typeof redisPreviewSchema>;

export interface ManagedRedisInitialCredentials {
  password: string;
}

export interface CreateManagedRedisInput {
  backupPolicy?: CreateBackupPolicyInput;
  cpuMillicores?: number;
  credentials: ManagedRedisInitialCredentials;
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
  ownerPassword: z.string().min(1),
  ownerUsername: z.string().min(1),
  port: z.literal(5432),
  portForward: portForwardSchema.optional(),
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

const postgresExtensionSchema = z.object({
  comment: z.string(),
  defaultVersion: z.string(),
  installedVersion: z.string().optional(),
  name: z.string().min(1),
});
const postgresExtensionsSchema = z.object({
  extensions: z.array(postgresExtensionSchema),
});

const managedPostgresSessionSchema = z.object({
  clientAddr: z.string(),
  durationMillis: z.number().nonnegative(),
  pid: z.number().int(),
  query: z.string(),
  state: z.string(),
  usename: z.string(),
  waitEvent: z.string(),
});

const managedPostgresStatementSchema = z.object({
  calls: z.number().int().nonnegative(),
  maxExecTimeMillis: z.number().nonnegative(),
  meanExecTimeMillis: z.number().nonnegative(),
  percentOfTotalTime: z.number().nonnegative(),
  query: z.string(),
  queryId: z.string(),
  rows: z.number().int().nonnegative(),
  sharedBlksHit: z.number().int().nonnegative(),
  sharedBlksRead: z.number().int().nonnegative(),
  tempBlksRead: z.number().int().nonnegative(),
  tempBlksWritten: z.number().int().nonnegative(),
  totalExecTimeMillis: z.number().nonnegative(),
});

const managedPostgresIndexSchema = z.object({
  idxScan: z.number().int().nonnegative(),
  idxTupFetch: z.number().int().nonnegative(),
  idxTupRead: z.number().int().nonnegative(),
  index: z.string().min(1),
  schema: z.string().min(1),
  sizeBytes: z.number().int().nonnegative(),
  sizePretty: z.string(),
  table: z.string().min(1),
});

const managedPostgresSequentialScanSchema = z.object({
  idxScan: z.number().int().nonnegative(),
  idxTupFetch: z.number().int().nonnegative(),
  nLiveTup: z.number().int().nonnegative(),
  seqScan: z.number().int().nonnegative(),
  seqTupRead: z.number().int().nonnegative(),
  sizeBytes: z.number().int().nonnegative(),
  sizePretty: z.string(),
  table: z.string().min(1),
});

const managedPostgresBlockedSchema = z.object({
  blockedForMillis: z.number().nonnegative(),
  blockedPid: z.number().int(),
  blockedQuery: z.string(),
  blockedUser: z.string(),
  blockingPid: z.number().int(),
  blockingQuery: z.string(),
  waitEvent: z.string(),
  waitEventType: z.string(),
});

const managedPostgresTableSchema = z.object({
  dataPretty: z.string(),
  deadRows: z.number().int().nonnegative(),
  indexesPretty: z.string(),
  lastVacuum: z.string(),
  rows: z.number().int().nonnegative(),
  table: z.string().min(1),
  totalPretty: z.string(),
});

const managedPostgresStatsSchema = z.object({
  active: z.number().int().nonnegative(),
  blksHit: z.number().int().nonnegative(),
  blksRead: z.number().int().nonnegative(),
  blockSizeBytes: z.number().int().nonnegative(),
  blocked: z.array(managedPostgresBlockedSchema),
  bytesHitPerSecond: z.number().nonnegative(),
  bytesReadPerSecond: z.number().nonnegative(),
  cacheHitPercent: z.number().nonnegative(),
  connections: z.number().int().nonnegative(),
  databaseSizeBytes: z.number().int().nonnegative(),
  idle: z.number().int().nonnegative(),
  idleInTransaction: z.number().int().nonnegative(),
  indexes: z.array(managedPostgresIndexSchema),
  meanQueryLatencyMillis: z.number().nonnegative(),
  queriesPerSecond: z.number().nonnegative(),
  rowsReadPerSecond: z.number().nonnegative(),
  rowsWrittenPerSecond: z.number().nonnegative(),
  sequentialScans: z.array(managedPostgresSequentialScanSchema),
  sessions: z.array(managedPostgresSessionSchema),
  statements: z.array(managedPostgresStatementSchema),
  statementsCalls: z.number().int().nonnegative(),
  statementsTotalExecTimeMillis: z.number().nonnegative(),
  tables: z.array(managedPostgresTableSchema),
  transactionsPerSecond: z.number().nonnegative(),
  tupDeleted: z.number().int().nonnegative(),
  tupFetched: z.number().int().nonnegative(),
  tupInserted: z.number().int().nonnegative(),
  tupReturned: z.number().int().nonnegative(),
  tupUpdated: z.number().int().nonnegative(),
  version: z.string().min(1),
  xactCommit: z.number().int().nonnegative(),
  xactRollback: z.number().int().nonnegative(),
});

const managedStatsHistoryPointSchema = z.object({
  metrics: z.record(z.string(), z.unknown()),
  observedAt: z.number().int().positive(),
});

const managedStatsHistoryTotalsSchema = z.object({
  bytesHit: z.number().optional(),
  bytesIn: z.number().optional(),
  bytesOut: z.number().optional(),
  bytesRead: z.number().optional(),
  commandCount: z.number().optional(),
  errorCount: z.number().optional(),
  netInputBytes: z.number().optional(),
  netOutputBytes: z.number().optional(),
  operationCount: z.number().optional(),
  otherQueryCount: z.number().optional(),
  queryCount: z.number().optional(),
  rowsRead: z.number().optional(),
  rowsWritten: z.number().optional(),
});

const managedStatsHistorySchema = z.object({
  from: z.number().int().positive(),
  points: z.array(managedStatsHistoryPointSchema),
  stepMillis: z.number().int().positive(),
  to: z.number().int().positive(),
  totals: managedStatsHistoryTotalsSchema,
});

export type ManagedPostgres = z.infer<typeof managedPostgresSchema>;
export type ManagedPostgresStats = z.infer<typeof managedPostgresStatsSchema>;
export type ManagedStatsHistory = z.infer<typeof managedStatsHistorySchema>;
export type ManagedStatsHistoryPoint = z.infer<
  typeof managedStatsHistoryPointSchema
>;
export type PostgresExtension = z.infer<typeof postgresExtensionSchema>;
export type PostgresQueryResult = z.infer<typeof postgresQueryResultSchema>;

export interface ManagedPostgresInitialCredentials {
  databaseName: string;
  ownerPassword: string;
  ownerUsername: string;
}

export interface CreateManagedPostgresInput {
  backupPolicy?: CreateBackupPolicyInput;
  cpuMillicores?: number;
  credentials: ManagedPostgresInitialCredentials;
  imageTag: string;
  memoryBytes?: number;
  name: string;
}

const objectStoreSchema = z.object({
  accessKey: z.string().min(1),
  backupCron: z.string().optional(),
  backupEnabled: z.boolean(),
  backupRetentionCount: z.number().int().min(1).max(100),
  bucketName: z.string().min(3),
  corsOrigins: z.array(z.string()),
  createdAt: z.number().int().positive(),
  credentialPermission: z.enum(["read", "read_write"]),
  id: z.string().min(1),
  internalHostname: z.string().min(1),
  name: z.string().min(1),
  portForward: portForwardSchema.optional(),
  projectId: z.string().min(1),
  publicHostname: z.string().min(1).optional(),
  region: z.literal("us-east-1"),
  secret: z.string().min(1),
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

const objectStoreTrafficSchema = z.object({
  activeRequests: z.number().int().nonnegative(),
  bytesIn: z.number().int().nonnegative(),
  bytesOut: z.number().int().nonnegative(),
  errors: z.number().int().nonnegative(),
  ops: z.record(z.string(), z.number().int().nonnegative()),
  totalLatencyMicros: z.number().int().nonnegative(),
});

const objectStoreStatsSchema = z.object({
  objectCount: z.number().int().nonnegative(),
  objectSizeHistogram: z.array(
    z.object({
      count: z.number().int().nonnegative(),
      label: z.string().min(1),
    })
  ),
  observedAt: z.number().int().positive().optional(),
  ready: z.boolean(),
  totalBytes: z.number().int().nonnegative(),
  traffic: objectStoreTrafficSchema.optional(),
});

const largestObjectsSearchSchema = z.object({
  error: z.string().min(1).optional(),
  objects: z.array(
    z.object({
      key: z.string().min(1),
      size: z.number().int().nonnegative(),
    })
  ),
  scannedObjects: z.number().int().nonnegative(),
  status: z.enum([
    "idle",
    "running",
    "cancelling",
    "complete",
    "cancelled",
    "failed",
  ]),
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
export type ObjectStoreStats = z.infer<typeof objectStoreStatsSchema>;
export type ObjectStoreTraffic = z.infer<typeof objectStoreTrafficSchema>;
export type LargestObjectsSearch = z.infer<typeof largestObjectsSearchSchema>;

export interface ObjectStoreInitialCredentials {
  accessKey: string;
  secret: string;
}

export interface CreateObjectStoreInput {
  backupPolicy?: CreateBackupPolicyInput;
  bucketName: string;
  corsOrigins: string[];
  credentials: ObjectStoreInitialCredentials;
  name: string;
  publicHostname?: string;
}

export interface CreateBackupPolicyInput {
  cron: string;
  enabled: boolean;
  retentionCount: number;
  targetId: string;
}

const backupTargetSchema = z.object({
  accessKeyId: z.string().min(1),
  bucket: z.string().min(1),
  createdAt: z.number().int().positive(),
  endpoint: z.string().min(1),
  id: z.string().min(1),
  name: z.string().min(1),
  prefix: z.string(),
  region: z.string().min(1),
  updatedAt: z.number().int().positive(),
});

const backupTargetsSchema = z.object({
  controlTargetId: z.string(),
  targets: z.array(backupTargetSchema),
});

export type BackupTarget = z.infer<typeof backupTargetSchema>;
export type BackupTargets = z.infer<typeof backupTargetsSchema>;

export interface SetBackupTargetInput {
  accessKeyId: string;
  bucket: string;
  endpoint: string;
  name: string;
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
  "image",
  "object_store",
  "postgres",
  "redis",
  "volume",
]);

const backupPolicySchema = z.object({
  cron: z.string().optional(),
  enabled: z.boolean(),
  nextRunAt: z.number().int().positive().optional(),
  resourceId: z.string().min(1),
  resourceKind: recoveryResourceKindSchema,
  retentionCount: z.number().int().min(1).max(100),
  targetId: z.string().optional(),
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
  targetId: z.string().min(1),
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
  readonly listener?: ServiceListener;

  constructor(
    code: string,
    message: string,
    domain?: ServiceDomain,
    listener?: ServiceListener
  ) {
    super(message);
    this.name = "APIError";
    this.code = code;
    this.domain = domain;
    this.listener = listener;
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
        parsed.data.error.domain,
        parsed.data.error.listener
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
  const identity = identitySchema.parse(await response.json());
  try {
    const profileResponse = await fetcher("/cdn-cgi/access/get-identity", {
      credentials: "same-origin",
      headers: { Accept: "application/json" },
      signal,
    });
    if (!profileResponse.ok) {
      return identity;
    }
    const parsedProfile = accessIdentityProfileSchema.safeParse(
      await profileResponse.json()
    );
    if (!parsedProfile.success) {
      return identity;
    }
    const name = identityDisplayName(parsedProfile.data);
    const avatarUrl = identityAvatarURL(parsedProfile.data);
    return {
      ...identity,
      ...(name ? { name } : {}),
      ...(avatarUrl ? { avatarUrl } : {}),
    };
  } catch (profileError) {
    if (signal?.aborted) {
      throw profileError;
    }
    return identity;
  }
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

export const projectIconURL = (project: Pick<Project, "id" | "updatedAt">) =>
  `/api/v1/projects/${encodeURIComponent(project.id)}/icon?v=${project.updatedAt}`;

export const uploadProjectIcon = async (
  projectID: string,
  file: Blob,
  fetcher: Fetcher = globalThis.fetch
): Promise<Project> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/icon`,
    {
      body: file,
      headers: { Accept: "application/json" },
      method: "PUT",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `project icon upload failed with ${response.status}`
    );
  }
  return projectSchema.parse(await response.json());
};

export const clearProjectIcon = async (
  projectID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<Project> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/icon`,
    {
      headers: { Accept: "application/json" },
      method: "DELETE",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `project icon clear failed with ${response.status}`
    );
  }
  return projectSchema.parse(await response.json());
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

export const deleteProject = async (
  projectID: string,
  input: { deleteBackups: boolean; expectedName: string },
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}`,
    {
      body: JSON.stringify(input),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "DELETE",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `project deletion failed with ${response.status}`
    );
  }
};

const projectWebhooksPath = (projectID: string) =>
  `/api/v1/projects/${encodeURIComponent(projectID)}/webhooks`;

export const fetchProjectWebhooks = async (
  projectID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<{
  eventTypes: ProjectWebhookEventType[];
  webhooks: ProjectWebhook[];
}> => {
  const response = await fetcher(projectWebhooksPath(projectID), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `project webhooks request failed with ${response.status}`
    );
  }
  return projectWebhooksSchema.parse(await response.json());
};

export const createProjectWebhook = async (
  projectID: string,
  input: { eventTypes: ProjectWebhookEventType[]; url: string },
  fetcher: Fetcher = globalThis.fetch
): Promise<ProjectWebhook> => {
  const response = await fetcher(projectWebhooksPath(projectID), {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `project webhook creation failed with ${response.status}`
    );
  }
  return projectWebhookSchema.parse(await response.json());
};

export const updateProjectWebhook = async (
  projectID: string,
  webhookID: string,
  input: { eventTypes: ProjectWebhookEventType[]; url: string },
  fetcher: Fetcher = globalThis.fetch
): Promise<ProjectWebhook> => {
  const response = await fetcher(
    `${projectWebhooksPath(projectID)}/${encodeURIComponent(webhookID)}`,
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
      `project webhook update failed with ${response.status}`
    );
  }
  return projectWebhookSchema.parse(await response.json());
};

export const deleteProjectWebhook = async (
  projectID: string,
  webhookID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `${projectWebhooksPath(projectID)}/${encodeURIComponent(webhookID)}`,
    { headers: { Accept: "application/json" }, method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `project webhook deletion failed with ${response.status}`
    );
  }
};

export const testProjectWebhook = async (
  projectID: string,
  url: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(`${projectWebhooksPath(projectID)}/test`, {
    body: JSON.stringify({ url }),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `project webhook test failed with ${response.status}`
    );
  }
};

export const fetchProjectCanvas = async (
  projectID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ProjectCanvas> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/canvas`,
    {
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `project canvas request failed with ${response.status}`
    );
  }
  return projectCanvasSchema.parse(await response.json());
};

export const fetchHostNetworkAddresses = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<HostNetworkAddress[]> => {
  const response = await fetcher("/api/v1/network/addresses", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(response, "host network addresses request failed");
  }
  return z
    .object({ addresses: z.array(hostNetworkAddressSchema) })
    .parse(await response.json()).addresses;
};

export const fetchNetworkGateway = async (
  projectID: string,
  gatewayID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<NetworkGateway> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/network-gateways/${encodeURIComponent(gatewayID)}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(response, "network gateway request failed");
  }
  return networkGatewaySchema.parse(await response.json());
};

const mutateNetworkGateway = async (
  projectID: string,
  input: NetworkGatewayInput,
  gatewayID: string | undefined,
  fetcher: Fetcher
): Promise<NetworkGateway> => {
  const suffix = gatewayID ? `/${encodeURIComponent(gatewayID)}` : "";
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/network-gateways${suffix}`,
    {
      body: JSON.stringify(networkGatewayInputSchema.parse(input)),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: gatewayID ? "PUT" : "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(response, "network gateway mutation failed");
  }
  return networkGatewaySchema.parse(await response.json());
};

export const createNetworkGateway = (
  projectID: string,
  input: NetworkGatewayInput,
  fetcher: Fetcher = globalThis.fetch
) => mutateNetworkGateway(projectID, input, undefined, fetcher);

export const updateNetworkGateway = (
  projectID: string,
  gatewayID: string,
  input: NetworkGatewayInput,
  fetcher: Fetcher = globalThis.fetch
) => mutateNetworkGateway(projectID, input, gatewayID, fetcher);

export const deleteNetworkGateway = async (
  projectID: string,
  gatewayID: string,
  fetcher: Fetcher = globalThis.fetch
) => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/network-gateways/${encodeURIComponent(gatewayID)}`,
    { method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(response, "network gateway deletion failed");
  }
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

const resolvedEnvironmentSchema = z.object({
  environment: z.record(z.string(), z.string()),
});

export const fetchResolvedServiceEnvironment = async (
  projectID: string,
  serviceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Record<string, string>> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/variables/resolved`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `resolved variables request failed with ${response.status}`
    );
  }
  return resolvedEnvironmentSchema.parse(await response.json()).environment;
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

export const deleteService = async (
  projectID: string,
  serviceID: string,
  expectedUpdatedAt: number,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}`,
    {
      body: JSON.stringify({ expectedUpdatedAt }),
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
      },
      method: "DELETE",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `service deletion failed with ${response.status}`
    );
  }
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
  action: "redeploy",
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

const serviceDeploymentAction = async (
  projectID: string,
  serviceID: string,
  deploymentID: string,
  action: "deploy" | "remove" | "restart",
  expectedUpdatedAt: number,
  fetcher: Fetcher = globalThis.fetch
): Promise<Service> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/deployments/${encodeURIComponent(deploymentID)}/${action}`,
    {
      body: JSON.stringify({ expectedUpdatedAt }),
      headers: { "Content-Type": "application/json" },
      method: "POST",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `${action} service deployment request failed with ${response.status}`
    );
  }
  return serviceSchema.parse(await response.json());
};

export const deployServiceVersion = (
  projectID: string,
  serviceID: string,
  deploymentID: string,
  expectedUpdatedAt: number,
  fetcher: Fetcher = globalThis.fetch
): Promise<Service> =>
  serviceDeploymentAction(
    projectID,
    serviceID,
    deploymentID,
    "deploy",
    expectedUpdatedAt,
    fetcher
  );

export const restartServiceDeployment = (
  projectID: string,
  serviceID: string,
  deploymentID: string,
  expectedUpdatedAt: number,
  fetcher?: Fetcher
) =>
  serviceDeploymentAction(
    projectID,
    serviceID,
    deploymentID,
    "restart",
    expectedUpdatedAt,
    fetcher
  );

export const removeServiceDeployment = (
  projectID: string,
  serviceID: string,
  deploymentID: string,
  expectedUpdatedAt: number,
  fetcher?: Fetcher
) =>
  serviceDeploymentAction(
    projectID,
    serviceID,
    deploymentID,
    "remove",
    expectedUpdatedAt,
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

export const fetchServicePreviews = async (
  projectID: string,
  serviceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<PreviewDeployment[]> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/previews`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(response, "image preview history request failed");
  }
  return z
    .object({ previews: z.array(previewDeploymentSchema) })
    .parse(await response.json()).previews;
};

export const fetchServiceDeployment = async (
  projectID: string,
  serviceID: string,
  deploymentID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Deployment> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/deployments/${encodeURIComponent(deploymentID)}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `deployment request failed with ${response.status}`
    );
  }
  return deploymentSchema.parse(await response.json());
};

export type ManagedDeploymentKind = "postgres" | "redis";

export const fetchRuntimeDeployments = async (
  projectID: string,
  kind: ManagedDeploymentKind,
  resourceID: string,
  cursor?: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RuntimeDeploymentPage> => {
  const query = new URLSearchParams({ limit: "50" });
  if (cursor) {
    query.set("cursor", cursor);
  }
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/${kind}/${encodeURIComponent(resourceID)}/deployments?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `deployment history request failed with ${response.status}`
    );
  }
  return runtimeDeploymentPageSchema.parse(await response.json());
};

export const fetchRuntimeDeployment = async (
  projectID: string,
  kind: ManagedDeploymentKind,
  resourceID: string,
  deploymentID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<RuntimeDeployment> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/${kind}/${encodeURIComponent(resourceID)}/deployments/${encodeURIComponent(deploymentID)}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `deployment request failed with ${response.status}`
    );
  }
  return runtimeDeploymentSchema.parse(await response.json());
};

const runtimeDeploymentAction = async (
  projectID: string,
  kind: ManagedDeploymentKind,
  resourceID: string,
  deploymentID: string,
  action: "remove" | "restart",
  fetcher: Fetcher = globalThis.fetch
) => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/${kind}/${encodeURIComponent(resourceID)}/deployments/${encodeURIComponent(deploymentID)}/${action}`,
    { method: "POST" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `${action} deployment request failed with ${response.status}`
    );
  }
};

export const restartRuntimeDeployment = (
  projectID: string,
  kind: ManagedDeploymentKind,
  resourceID: string,
  deploymentID: string,
  fetcher?: Fetcher
) =>
  runtimeDeploymentAction(
    projectID,
    kind,
    resourceID,
    deploymentID,
    "restart",
    fetcher
  );

export const removeRuntimeDeployment = (
  projectID: string,
  kind: ManagedDeploymentKind,
  resourceID: string,
  deploymentID: string,
  fetcher?: Fetcher
) =>
  runtimeDeploymentAction(
    projectID,
    kind,
    resourceID,
    deploymentID,
    "remove",
    fetcher
  );

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

export const fetchBuildLog = async (
  projectID: string,
  serviceID: string,
  deploymentID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<string> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/deployments/${encodeURIComponent(deploymentID)}/logs/build`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `build log request failed with ${response.status}`
    );
  }
  return buildLogSchema.parse(await response.json()).text;
};

export type ResourceLogKind = "object_store" | "postgres" | "redis" | "service";

const resourceLogCollection: Record<ResourceLogKind, string> = {
  object_store: "object-stores",
  postgres: "postgres",
  redis: "redis",
  service: "services",
};

export const fetchResourceLogs = async (
  projectID: string,
  kind: ResourceLogKind,
  resourceID: string,
  options: { contains?: string; deploymentId?: string; limit?: number } = {},
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<LogWindow> => {
  const query = new URLSearchParams({ limit: String(options.limit ?? 500) });
  if (options.contains) {
    query.set("contains", options.contains);
  }
  if (options.deploymentId) {
    query.set("deploymentId", options.deploymentId);
  }
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/${resourceLogCollection[kind]}/${encodeURIComponent(resourceID)}/logs?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `resource logs request failed with ${response.status}`
    );
  }
  return logWindowSchema.parse(await response.json());
};

export type ContainerResourceKind = "postgres" | "redis" | "service";

export const fetchResourceTerminalShells = async (
  projectID: string,
  resourceKind: ContainerResourceKind,
  resourceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<string[]> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/resources/${encodeURIComponent(resourceKind)}/${encodeURIComponent(resourceID)}/terminal/shells`,
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

const containerFileEntrySchema = z.object({
  directory: z.boolean(),
  mode: z.number().int().nonnegative(),
  modifiedAt: z.string(),
  path: z.string(),
  sizeBytes: z.number().int().nonnegative(),
});

const containerFileTreeSchema = z.object({
  entries: z.array(containerFileEntrySchema),
  root: z.string(),
});

export type ContainerFileEntry = z.infer<typeof containerFileEntrySchema>;
export type ContainerFileTree = z.infer<typeof containerFileTreeSchema>;

const resourceFilesPath = (
  projectID: string,
  resourceKind: ContainerResourceKind,
  resourceID: string
) =>
  `/api/v1/projects/${encodeURIComponent(projectID)}/resources/${encodeURIComponent(resourceKind)}/${encodeURIComponent(resourceID)}/files`;

export const fetchContainerFiles = async (
  projectID: string,
  resourceKind: ContainerResourceKind,
  resourceID: string,
  path: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ContainerFileTree> => {
  const query = new URLSearchParams({ path });
  const response = await fetcher(
    `${resourceFilesPath(projectID, resourceKind, resourceID)}?${query.toString()}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `container files request failed with ${response.status}`
    );
  }
  return containerFileTreeSchema.parse(await response.json());
};

export const containerFileContentURL = (
  projectID: string,
  resourceKind: ContainerResourceKind,
  resourceID: string,
  path: string
) => {
  const query = new URLSearchParams({ path });
  return `${resourceFilesPath(projectID, resourceKind, resourceID)}/content?${query.toString()}`;
};

export const uploadContainerFile = async (
  projectID: string,
  resourceKind: ContainerResourceKind,
  resourceID: string,
  path: string,
  file: File,
  fetcher: Fetcher = globalThis.fetch
) => {
  const response = await fetcher(
    containerFileContentURL(projectID, resourceKind, resourceID, path),
    { body: file, method: "PUT" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `container file upload failed with ${response.status}`
    );
  }
};

export const issueServerTerminalToken = async (
  passphrase: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<ServerTerminalToken> => {
  const response = await fetcher("/api/v1/server/terminal-token", {
    body: JSON.stringify({ passphrase }),
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
    },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `server terminal authorization failed with ${response.status}`
    );
  }
  return serverTerminalTokenSchema.parse(await response.json());
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

export const forceImageGarbageCollection = async (
  fetcher: Fetcher = globalThis.fetch
): Promise<ImageGarbageCollectionResult> => {
  const response = await fetcher("/api/v1/infrastructure/container-images/gc", {
    headers: { Accept: "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `container image garbage collection failed with ${response.status}`
    );
  }
  return imageGarbageCollectionResultSchema.parse(await response.json());
};

export const fetchInfrastructureLogs = async (
  query: InfrastructureLogsQuery = {},
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<InfrastructureLogWindow> => {
  const parameters = new URLSearchParams({ limit: String(query.limit ?? 500) });
  if (query.beforeCursor) {
    parameters.set("beforeCursor", query.beforeCursor);
  }
  const response = await fetcher(`/api/v1/infrastructure/logs?${parameters}`, {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `infrastructure logs request failed with ${response.status}`
    );
  }
  return infrastructureLogWindowSchema.parse(await response.json());
};

export const fetchResourceUsage = async (
  kind: ResourceUsageKind,
  resourceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ResourceUsage> => {
  const response = await fetcher(
    `/api/v1/infrastructure/resources/${kind}/${encodeURIComponent(resourceID)}/usage`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `resource usage request failed with ${response.status}`
    );
  }
  return resourceUsageSchema.parse(await response.json());
};

export const fetchResourceUsageHistory = async (
  kind: ResourceUsageKind,
  resourceID: string,
  range: ResourceUsageRange,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ResourceUsageHistory> => {
  const response = await fetcher(
    `/api/v1/infrastructure/resources/${kind}/${encodeURIComponent(resourceID)}/usage/history?range=${range}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `resource usage history request failed with ${response.status}`
    );
  }
  return resourceUsageHistorySchema.parse(await response.json());
};

const fetchUsage = async (
  path: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ResourceUsage> => {
  const response = await fetcher(path, {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `usage request failed with ${response.status}`
    );
  }
  return resourceUsageSchema.parse(await response.json());
};

const fetchUsageHistory = async (
  path: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ResourceUsageHistory> => {
  const response = await fetcher(path, {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `usage history request failed with ${response.status}`
    );
  }
  return resourceUsageHistorySchema.parse(await response.json());
};

export const fetchProjectUsage = (
  projectID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
) =>
  fetchUsage(
    `/api/v1/infrastructure/projects/${encodeURIComponent(projectID)}/usage`,
    signal,
    fetcher
  );

export const fetchProjectUsageHistory = (
  projectID: string,
  range: ResourceUsageRange,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
) =>
  fetchUsageHistory(
    `/api/v1/infrastructure/projects/${encodeURIComponent(projectID)}/usage/history?range=${range}`,
    signal,
    fetcher
  );

export const fetchInstallationUsage = (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
) => fetchUsage("/api/v1/infrastructure/usage", signal, fetcher);

export const fetchInstallationUsageHistory = (
  range: ResourceUsageRange,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
) =>
  fetchUsageHistory(
    `/api/v1/infrastructure/usage/history?range=${range}`,
    signal,
    fetcher
  );

export const fetchHostUsageHistory = (
  range: ResourceUsageRange,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
) =>
  fetchUsageHistory(
    `/api/v1/infrastructure/host/usage/history?range=${range}`,
    signal,
    fetcher
  );

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

export const fetchSelfUpdateStatus = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<SelfUpdateStatus> => {
  const response = await fetcher("/api/v1/infrastructure/update", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `platform update check failed with ${response.status}`
    );
  }
  return selfUpdateStatusSchema.parse(await response.json());
};

export const fetchAuditEvents = async (
  filters: {
    action?: string;
    actorKind?: AuditEvent["actorKind"];
    cursor?: string;
    limit?: number;
    projectId?: string;
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
  if (filters.projectId) {
    query.set("projectId", filters.projectId);
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
    {
      headers: { Accept: "application/json" },
      signal,
    }
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

export const updateManagedRedisPortForward = async (
  projectID: string,
  redisID: string,
  input: {
    expectedUpdatedAt: number;
    portForward?: PortForwardAccess;
  },
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedRedis> => {
  const response = await fetcher(
    `${managedRedisPath(projectID, redisID)}/port-forward`,
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
      `managed Redis port-forward update failed with ${response.status}`
    );
  }
  return managedRedisSchema.parse(await response.json());
};

export const fetchManagedRedisPersistence = async (
  projectID: string,
  redisID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedRedisPersistence> => {
  const response = await fetcher(
    `${managedRedisPath(projectID, redisID)}/persistence`,
    {
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed Redis persistence request failed with ${response.status}`
    );
  }
  return managedRedisPersistenceSchema.parse(await response.json());
};

export const fetchManagedRedisStats = async (
  projectID: string,
  redisID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedRedisStats> => {
  const response = await fetcher(
    `${managedRedisPath(projectID, redisID)}/stats`,
    {
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed Redis stats request failed with ${response.status}`
    );
  }
  return managedRedisStatsSchema.parse(await response.json());
};

export const fetchManagedRedisStatsHistory = async (
  projectID: string,
  redisID: string,
  range: ResourceUsageRange,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedStatsHistory> => {
  const response = await fetcher(
    `${managedRedisPath(projectID, redisID)}/stats/history?range=${range}`,
    {
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed Redis stats history request failed with ${response.status}`
    );
  }
  return managedStatsHistorySchema.parse(await response.json());
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

export const updateManagedPostgresPortForward = async (
  projectID: string,
  postgresID: string,
  input: {
    expectedUpdatedAt: number;
    portForward?: PortForwardAccess;
  },
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedPostgres> => {
  const response = await fetcher(
    `${managedPostgresPath(projectID, postgresID)}/port-forward`,
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
      `managed PostgreSQL port-forward update failed with ${response.status}`
    );
  }
  return managedPostgresSchema.parse(await response.json());
};

export const queryManagedPostgres = async (
  projectID: string,
  postgresID: string,
  sql: string,
  signal?: AbortSignal,
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
      signal,
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

export const fetchManagedPostgresStats = async (
  projectID: string,
  postgresID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedPostgresStats> => {
  const response = await fetcher(
    `${managedPostgresPath(projectID, postgresID)}/stats`,
    {
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed PostgreSQL stats request failed with ${response.status}`
    );
  }
  return managedPostgresStatsSchema.parse(await response.json());
};

export const fetchManagedPostgresStatsHistory = async (
  projectID: string,
  postgresID: string,
  range: ResourceUsageRange,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedStatsHistory> => {
  const response = await fetcher(
    `${managedPostgresPath(projectID, postgresID)}/stats/history?range=${range}`,
    {
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed PostgreSQL stats history request failed with ${response.status}`
    );
  }
  return managedStatsHistorySchema.parse(await response.json());
};

export const fetchManagedPostgresExtensions = async (
  projectID: string,
  postgresID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<PostgresExtension[]> => {
  const response = await fetcher(
    `${managedPostgresPath(projectID, postgresID)}/extensions`,
    {
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed PostgreSQL extensions request failed with ${response.status}`
    );
  }
  return postgresExtensionsSchema.parse(await response.json()).extensions;
};

export const setManagedPostgresExtension = async (
  projectID: string,
  postgresID: string,
  name: string,
  installed: boolean,
  fetcher: Fetcher = globalThis.fetch
): Promise<Operation> => {
  const response = await fetcher(
    `${managedPostgresPath(projectID, postgresID)}/extensions/${encodeURIComponent(name)}`,
    {
      headers: { Accept: "application/json" },
      method: installed ? "PUT" : "DELETE",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `managed PostgreSQL extension change failed with ${response.status}`
    );
  }
  return operationSchema.parse(await response.json());
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

export const updateObjectStorePortForward = async (
  projectID: string,
  storeID: string,
  input: {
    expectedUpdatedAt: number;
    portForward?: PortForwardAccess;
  },
  fetcher: Fetcher = globalThis.fetch
): Promise<ObjectStore> => {
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/port-forward`,
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
      `object store port-forward update failed with ${response.status}`
    );
  }
  return objectStoreSchema.parse(await response.json());
};

export const updateObjectStorePublicAccess = async (
  projectID: string,
  storeID: string,
  input: {
    corsOrigins: string[];
    expectedUpdatedAt: number;
    publicHostname?: string;
  },
  fetcher: Fetcher = globalThis.fetch
): Promise<ObjectStore> => {
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/public-access`,
    {
      body: JSON.stringify({
        corsOrigins: input.corsOrigins,
        expectedUpdatedAt: input.expectedUpdatedAt,
        publicHostname: input.publicHostname ?? "",
      }),
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
      `object store public-access update failed with ${response.status}`
    );
  }
  return objectStoreSchema.parse(await response.json());
};

export const fetchObjectStoreStats = async (
  projectID: string,
  storeID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ObjectStoreStats> => {
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/stats`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `object store stats request failed with ${response.status}`
    );
  }
  return objectStoreStatsSchema.parse(await response.json());
};

export const fetchObjectStoreStatsHistory = async (
  projectID: string,
  storeID: string,
  range: ResourceUsageRange,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ManagedStatsHistory> => {
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/stats/history?range=${range}`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `object store stats history request failed with ${response.status}`
    );
  }
  return managedStatsHistorySchema.parse(await response.json());
};

const largestObjectsRequest = async (
  projectID: string,
  storeID: string,
  method: "DELETE" | "GET" | "POST",
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<LargestObjectsSearch> => {
  const response = await fetcher(
    `${objectStorePath(projectID, storeID)}/largest-objects`,
    { headers: { Accept: "application/json" }, method, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `largest objects request failed with ${response.status}`
    );
  }
  return largestObjectsSearchSchema.parse(await response.json());
};

export const fetchLargestObjectsSearch = (
  projectID: string,
  storeID: string,
  signal?: AbortSignal,
  fetcher?: Fetcher
) => largestObjectsRequest(projectID, storeID, "GET", signal, fetcher);

export const startLargestObjectsSearch = (
  projectID: string,
  storeID: string,
  signal?: AbortSignal,
  fetcher?: Fetcher
) => largestObjectsRequest(projectID, storeID, "POST", signal, fetcher);

export const cancelLargestObjectsSearch = (
  projectID: string,
  storeID: string,
  signal?: AbortSignal,
  fetcher?: Fetcher
) => largestObjectsRequest(projectID, storeID, "DELETE", signal, fetcher);

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

export const fetchBackupTargets = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupTargets> => {
  const response = await fetcher("/api/v1/backups/targets", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup target request failed with ${response.status}`
    );
  }
  return backupTargetsSchema.parse(await response.json());
};

export const createBackupTarget = async (
  input: SetBackupTargetInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupTarget> => {
  const response = await fetcher("/api/v1/backups/targets", {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup target update failed with ${response.status}`
    );
  }
  return backupTargetSchema.parse(await response.json());
};

export const updateBackupTarget = async (
  targetID: string,
  input: SetBackupTargetInput,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupTarget> => {
  const response = await fetcher(
    `/api/v1/backups/targets/${encodeURIComponent(targetID)}`,
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
      `Backup target update failed with ${response.status}`
    );
  }
  return backupTargetSchema.parse(await response.json());
};

export const deleteBackupTarget = async (
  targetID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `/api/v1/backups/targets/${encodeURIComponent(targetID)}`,
    {
      method: "DELETE",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup target deletion failed with ${response.status}`
    );
  }
};

export const setControlBackupTarget = async (
  targetID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<string> => {
  const response = await fetcher("/api/v1/backups/control-target", {
    body: JSON.stringify({ targetId: targetID }),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "PUT",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Disaster recovery target update failed with ${response.status}`
    );
  }
  return z.object({ targetId: z.string() }).parse(await response.json())
    .targetId;
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

export const fetchBackupPolicy = async (
  kind: RecoveryResourceKind,
  resourceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupPolicy> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/policy`,
    {
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `Backup policy request failed with ${response.status}`
    );
  }
  return backupPolicySchema.parse(await response.json());
};

export const setBackupPolicy = async (
  kind: RecoveryResourceKind,
  resourceID: string,
  input: {
    cron: string;
    enabled: boolean;
    retentionCount: number;
    targetId: string;
  },
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
  targetID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupRecord> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/run`,
    {
      body: JSON.stringify({ targetId: targetID }),
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
      `Backup request failed with ${response.status}`
    );
  }
  return backupRecordSchema.parse(await response.json());
};

export const fetchBackupHistory = async (
  kind: RecoveryResourceKind,
  resourceID: string,
  targetID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupRecord[]> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/history?${new URLSearchParams({ limit: "50", targetId: targetID })}`,
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
  targetID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<BackupGeneration[]> => {
  const response = await fetcher(
    `${backupResourcePath(kind, resourceID)}/generations?${new URLSearchParams({ targetId: targetID })}`,
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
  targetID: string,
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
        targetId: targetID,
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
    {
      headers: { Accept: "application/json" },
      signal,
    }
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

export const fetchServiceDomainDNSStatus = async (
  projectID: string,
  serviceID: string,
  hostname: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ServiceDomainDNSStatus> => {
  const response = await fetcher(
    `${serviceDomainsPath(projectID, serviceID)}/${encodeURIComponent(hostname)}/dns`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `domain DNS status request failed with ${response.status}`
    );
  }
  return serviceDomainDNSStatusSchema.parse(await response.json()).status;
};

export const attachServiceDomain = async (
  projectID: string,
  serviceID: string,
  hostname: string,
  targetPort: number,
  move = false,
  fetcher: Fetcher = globalThis.fetch
): Promise<ServiceDomain> => {
  const response = await fetcher(serviceDomainsPath(projectID, serviceID), {
    body: JSON.stringify({ hostname, move, targetPort }),
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

const serviceListenersPath = (projectID: string, serviceID: string) =>
  `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/listeners`;

export const fetchServiceListeners = async (
  projectID: string,
  serviceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ServiceListener[]> => {
  const response = await fetcher(serviceListenersPath(projectID, serviceID), {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `service listeners request failed with ${response.status}`
    );
  }
  return serviceListenersSchema.parse(await response.json()).listeners;
};

export const fetchContainerPorts = async (
  projectID: string,
  resourceKind: "postgres" | "redis" | "service",
  resourceID: string,
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<ContainerPort[]> => {
  const response = await fetcher(
    `/api/v1/projects/${encodeURIComponent(projectID)}/resources/${resourceKind}/${encodeURIComponent(resourceID)}/ports`,
    { headers: { Accept: "application/json" }, signal }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `container ports request failed with ${response.status}`
    );
  }
  return containerPortsSchema.parse(await response.json()).ports;
};

export const attachServiceListener = async (
  projectID: string,
  serviceID: string,
  input: Pick<ServiceListener, "protocol" | "publicPort" | "targetPort">,
  fetcher: Fetcher = globalThis.fetch
): Promise<ServiceListener> => {
  const response = await fetcher(serviceListenersPath(projectID, serviceID), {
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
      `listener attachment failed with ${response.status}`
    );
  }
  return serviceListenerSchema.parse(await response.json());
};

export const detachServiceListener = async (
  projectID: string,
  serviceID: string,
  protocol: ServiceListener["protocol"],
  publicPort: number,
  fetcher: Fetcher = globalThis.fetch
): Promise<void> => {
  const response = await fetcher(
    `${serviceListenersPath(projectID, serviceID)}/${protocol}/${publicPort}`,
    { method: "DELETE" }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `listener removal failed with ${response.status}`
    );
  }
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
    {
      method: "DELETE",
    }
  );
  if (!response.ok) {
    throw await apiError(
      response,
      `API token revoke failed with ${response.status}`
    );
  }
};

const originCertificateSettingsSchema = z.object({
  createdAt: z.number().int().positive(),
  dnsNames: z.array(z.string().min(1)),
  id: z.string().min(1),
});

const installationSettingsSchema = z.object({
  accessAudience: z.string().min(1),
  accessTeamDomain: z.string().min(1),
  adminHostname: z.string().min(1),
  certificates: z.array(originCertificateSettingsSchema),
  installationId: z.string().min(1),
});

export type InstallationSettings = z.infer<typeof installationSettingsSchema>;

const settingsPath = "/api/v1/settings";

export const fetchInstallationSettings = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<InstallationSettings> => {
  const response = await fetcher(settingsPath, {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Installation settings request failed with ${response.status}`
    );
  }
  return installationSettingsSchema.parse(await response.json());
};

export const setAdminHostname = async (
  hostname: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<InstallationSettings> => {
  const response = await fetcher(`${settingsPath}/admin-hostname`, {
    body: JSON.stringify({ hostname }),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "PUT",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Admin hostname update failed with ${response.status}`
    );
  }
  return installationSettingsSchema.parse(await response.json());
};

export const setCloudflareAccessConfiguration = async (
  input: { audience: string; teamDomain: string },
  fetcher: Fetcher = globalThis.fetch
): Promise<InstallationSettings> => {
  const response = await fetcher(`${settingsPath}/cloudflare-access`, {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "PUT",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Cloudflare Access update failed with ${response.status}`
    );
  }
  return installationSettingsSchema.parse(await response.json());
};

const originCertificatesPath = (certificateID?: string) =>
  `${settingsPath}/origin-certificates${certificateID ? `/${encodeURIComponent(certificateID)}` : ""}`;

export const addOriginCertificate = async (
  input: { certificatePem: string; privateKeyPem: string },
  fetcher: Fetcher = globalThis.fetch
): Promise<InstallationSettings> => {
  const response = await fetcher(originCertificatesPath(), {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Origin certificate creation failed with ${response.status}`
    );
  }
  return installationSettingsSchema.parse(await response.json());
};

export const replaceOriginCertificate = async (
  certificateID: string,
  input: { certificatePem: string; privateKeyPem: string },
  fetcher: Fetcher = globalThis.fetch
): Promise<InstallationSettings> => {
  const response = await fetcher(originCertificatesPath(certificateID), {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "PUT",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Origin certificate replacement failed with ${response.status}`
    );
  }
  return installationSettingsSchema.parse(await response.json());
};

export const deleteOriginCertificate = async (
  certificateID: string,
  fetcher: Fetcher = globalThis.fetch
): Promise<InstallationSettings> => {
  const response = await fetcher(originCertificatesPath(certificateID), {
    method: "DELETE",
  });
  if (!response.ok) {
    throw await apiError(
      response,
      `Origin certificate deletion failed with ${response.status}`
    );
  }
  return installationSettingsSchema.parse(await response.json());
};

const cloudflareDNSSettingsSchema = z.object({
  configured: z.boolean(),
  updatedAt: z.number().int().nonnegative(),
});

export type CloudflareDNSSettings = z.infer<typeof cloudflareDNSSettingsSchema>;

const cloudflareMeshSettingsSchema = z.object({
  accountId: z.string(),
  configured: z.boolean(),
  interfaceName: z.string(),
  meshIp: z.string(),
  nodeId: z.string(),
  nodeName: z.string(),
  status: z.enum(["connected", "disconnected", "not_configured"]),
  updatedAt: z.number().int().nonnegative(),
});

const cloudflareMeshCredentialSchema = z.object({
  accountId: z.string().min(1),
  apiToken: z.string().min(1),
});

export type CloudflareMeshSettings = z.infer<
  typeof cloudflareMeshSettingsSchema
>;
export type CloudflareMeshCredential = z.infer<
  typeof cloudflareMeshCredentialSchema
>;

export const fetchCloudflareDNSSettings = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<CloudflareDNSSettings> => {
  const response = await fetcher("/api/v1/settings/cloudflare", {
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(response, "Cloudflare DNS settings request failed");
  }
  return cloudflareDNSSettingsSchema.parse(await response.json());
};

export const configureCloudflareDNS = async (
  input: { apiToken: string },
  fetcher: Fetcher = globalThis.fetch
): Promise<CloudflareDNSSettings> => {
  const response = await fetcher("/api/v1/settings/cloudflare", {
    body: JSON.stringify(input),
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "PUT",
  });
  if (!response.ok) {
    throw await apiError(response, "Cloudflare DNS configuration failed");
  }
  return cloudflareDNSSettingsSchema.parse(await response.json());
};

export const fetchCloudflareMeshSettings = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<CloudflareMeshSettings> => {
  const response = await fetcher("/api/v1/settings/cloudflare-mesh", {
    cache: "no-store",
    headers: { Accept: "application/json" },
    signal,
  });
  if (!response.ok) {
    throw await apiError(response, "Cloudflare Mesh settings request failed");
  }
  return cloudflareMeshSettingsSchema.parse(await response.json());
};

export const fetchCloudflareMeshCredential = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<CloudflareMeshCredential> => {
  const response = await fetcher(
    "/api/v1/settings/cloudflare-mesh/credential",
    {
      cache: "no-store",
      headers: { Accept: "application/json" },
      signal,
    }
  );
  if (!response.ok) {
    throw await apiError(response, "Cloudflare Mesh credential request failed");
  }
  return cloudflareMeshCredentialSchema.parse(await response.json());
};

export const configureCloudflareMesh = async (
  input: { accountId: string; apiToken: string },
  fetcher: Fetcher = globalThis.fetch
): Promise<CloudflareMeshSettings> => {
  const response = await fetcher("/api/v1/settings/cloudflare-mesh", {
    body: JSON.stringify(input),
    cache: "no-store",
    headers: { Accept: "application/json", "Content-Type": "application/json" },
    method: "PUT",
  });
  if (!response.ok) {
    throw await apiError(response, "Cloudflare Mesh configuration failed");
  }
  return cloudflareMeshSettingsSchema.parse(await response.json());
};

export const reconnectCloudflareMesh = async (
  fetcher: Fetcher = globalThis.fetch
): Promise<CloudflareMeshSettings> => {
  const response = await fetcher("/api/v1/settings/cloudflare-mesh/connect", {
    cache: "no-store",
    headers: { Accept: "application/json" },
    method: "POST",
  });
  if (!response.ok) {
    throw await apiError(response, "Cloudflare Mesh connection failed");
  }
  return cloudflareMeshSettingsSchema.parse(await response.json());
};
