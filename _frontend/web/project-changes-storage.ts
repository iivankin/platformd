import { z } from "zod";

import type { PendingResourceCreation } from "@/pending-resource-creation";
import type { PendingServiceSettings } from "@/service-settings-model";

export type ProjectServiceChanges = Record<string, PendingServiceSettings>;
export type AllProjectChanges = Record<string, ProjectServiceChanges>;
export type ProjectResourceDrafts = Record<string, PendingResourceCreation>;
export type AllProjectResourceDrafts = Record<string, ProjectResourceDrafts>;

export interface StoredProjectChanges {
  changes: AllProjectChanges;
  resourceDrafts: AllProjectResourceDrafts;
}

interface ProjectChangesStorage {
  getItem: (key: string) => string | null;
  removeItem: (key: string) => void;
  setItem: (key: string, value: string) => void;
}

const storageKey = "platformd.project-changes:v1";
const environmentSchema = z.record(z.string(), z.string());
const sourceSchema = z.union([
  z.object({
    autoUpdate: z.boolean(),
    image: z.object({ reference: z.string() }),
    type: z.literal("platformd_registry"),
  }),
  z.object({
    autoUpdate: z.boolean(),
    image: z.object({ reference: z.string() }),
    minimumReleaseAgeDays: z.number().int().positive().max(36_500).optional(),
    type: z.enum(["public_image", "private_image"]),
  }),
  z.object({
    github: z.object({
      branch: z.string(),
      contextPath: z.string(),
      dockerfilePath: z.string(),
      pullRequestPreview: z.object({ hostnameTemplate: z.string() }).optional(),
      repository: z.string(),
      repositoryId: z.number(),
      revision: z.string().optional(),
      triggerPaths: z.array(z.string()),
      waitForCi: z.boolean(),
    }),
    type: z.literal("github"),
  }),
]);
const healthCheckSchema = z.object({
  path: z.string(),
  port: z.number(),
  timeoutSeconds: z.number(),
});
const githubWorkflowSchema = z.object({
  inputs: z.record(z.string(), z.unknown()),
  name: z.string(),
  path: z.string(),
});
const beforeDeploySchema = z.object({
  cloudflareHostnames: z.array(z.string()),
  command: z.string().optional(),
  githubWorkflow: githubWorkflowSchema.optional(),
});
const beforeDeployDraftSchema = z.object({
  cloudflareEnabled: z.boolean(),
  cloudflareHostnames: z.array(z.string()),
  command: z.string(),
  commandEnabled: z.boolean(),
  githubInputs: z.string(),
  githubWorkflow: githubWorkflowSchema.optional(),
  githubWorkflowEnabled: z.boolean(),
});
const registryCredentialSchema = z.object({
  password: z.string(),
  registryHost: z.string(),
  username: z.string(),
});
const volumeMountSchema = z.object({
  containerPath: z.string(),
  volumeId: z.string(),
});
const volumeSchema = z.object({
  createdAt: z.number(),
  id: z.string(),
  name: z.string(),
  projectId: z.string(),
  serviceId: z.string(),
});
const serviceSchema = z.object({
  activeConfigHash: z.string().optional(),
  activeDeploymentId: z.string().optional(),
  activeImageDigest: z.string().optional(),
  args: z.array(z.string()).optional(),
  beforeDeploy: beforeDeploySchema.optional(),
  buildEnvironment: environmentSchema,
  command: z.array(z.string()).optional(),
  cpuMillicores: z.number().optional(),
  createdAt: z.number(),
  enabled: z.boolean(),
  environment: environmentSchema,
  healthCheck: healthCheckSchema.optional(),
  id: z.string(),
  memoryMaxBytes: z.number().optional(),
  name: z.string(),
  projectId: z.string(),
  registryCredential: registryCredentialSchema.optional(),
  secretReferences: z.array(
    z.object({ environmentName: z.string(), secretId: z.string() })
  ),
  source: sourceSchema,
  updatedAt: z.number(),
  volumeMounts: z.array(volumeMountSchema),
});
const domainDraftSchema = z.object({
  hostname: z.string(),
  targetPort: z.number(),
});
const listenerDraftSchema = z.object({
  protocol: z.enum(["tcp", "udp"]),
  publicPort: z.number(),
  targetPort: z.number(),
});
const configurationDraftSchema = z.object({
  healthEnabled: z.boolean(),
  healthPath: z.string(),
  healthPort: z.string(),
  healthTimeout: z.string(),
  registryCredential: z.object({
    password: z.string(),
    username: z.string(),
  }),
  source: sourceSchema,
});
const volumeDraftSchema = volumeSchema.extend({
  pendingCreation: z.boolean().optional(),
});
const serviceSettingsDraftSchema = z.object({
  beforeDeploy: beforeDeployDraftSchema,
  configuration: configurationDraftSchema,
  domains: z.array(domainDraftSchema),
  listeners: z.array(listenerDraftSchema),
  volumeMounts: z.array(volumeMountSchema),
  volumes: z.array(volumeDraftSchema),
});
const pendingServiceSettingsSchema = z.object({
  baseline: z.object({
    domains: z.array(domainDraftSchema),
    listeners: z.array(listenerDraftSchema),
    service: serviceSchema,
    volumes: z.array(volumeSchema),
  }),
  buildEnvironment: environmentSchema,
  draft: serviceSettingsDraftSchema,
  environment: environmentSchema,
  serviceID: z.string(),
  serviceName: z.string(),
});
const backupPolicySchema = z.object({
  cron: z.string(),
  enabled: z.boolean(),
  retentionCount: z.number(),
  targetId: z.string(),
});
const managedResourceInputSchema = z.object({
  backupPolicy: backupPolicySchema.optional(),
  cpuMillicores: z.number().optional(),
  credentials: z.object({ password: z.string() }),
  imageTag: z.string(),
  memoryBytes: z.number().optional(),
  name: z.string(),
});
const postgresInputSchema = managedResourceInputSchema.extend({
  credentials: z.object({
    databaseName: z.string(),
    ownerPassword: z.string(),
    ownerUsername: z.string(),
  }),
});
const objectStoreInputSchema = z.object({
  backupPolicy: backupPolicySchema.optional(),
  bucketName: z.string(),
  corsOrigins: z.array(z.string()),
  credentials: z.object({ accessKey: z.string(), secret: z.string() }),
  name: z.string(),
  publicHostname: z.string().optional(),
});
const networkGatewayInputSchema = z.object({
  interfaceName: z.string(),
  listenPort: z.number(),
  mode: z.enum(["import", "export"]),
  name: z.string(),
  protocol: z.enum(["tcp", "udp"]),
  remoteHost: z.string(),
  remotePort: z.number(),
  sourceAddress: z.string(),
  targetPort: z.number(),
  targetServiceId: z.string(),
  transport: z.enum(["vpc", "mesh"]),
});
const createServiceInputSchema = z.object({
  beforeDeploy: beforeDeploySchema.optional(),
  buildEnvironment: environmentSchema,
  domains: z.array(domainDraftSchema).optional(),
  environment: environmentSchema,
  healthCheck: healthCheckSchema.optional(),
  listeners: z.array(listenerDraftSchema).optional(),
  name: z.string(),
  registryCredential: z
    .object({ password: z.string(), username: z.string() })
    .optional(),
  source: sourceSchema,
  volumes: z
    .array(z.object({ containerPath: z.string().optional(), name: z.string() }))
    .optional(),
});
const pendingResourceCreationSchema = z.discriminatedUnion("kind", [
  z.object({
    id: z.string(),
    input: networkGatewayInputSchema,
    kind: z.literal("network_gateway"),
  }),
  z.object({
    backupPolicy: backupPolicySchema,
    id: z.string(),
    input: postgresInputSchema,
    kind: z.literal("postgres"),
  }),
  z.object({
    backupPolicy: backupPolicySchema,
    id: z.string(),
    input: managedResourceInputSchema,
    kind: z.literal("redis"),
  }),
  z.object({
    backupPolicy: backupPolicySchema,
    id: z.string(),
    input: objectStoreInputSchema,
    kind: z.literal("storage"),
  }),
  z.object({
    id: z.string(),
    input: createServiceInputSchema,
    kind: z.literal("service"),
    settings: serviceSettingsDraftSchema,
  }),
]);
const storedProjectChangesSchema = z.object({
  changes: z.record(
    z.string(),
    z.record(z.string(), pendingServiceSettingsSchema)
  ),
  resourceDrafts: z.record(
    z.string(),
    z.record(z.string(), pendingResourceCreationSchema)
  ),
  version: z.literal(1),
});

export const emptyStoredProjectChanges = (): StoredProjectChanges => ({
  changes: {},
  resourceDrafts: {},
});

export const loadStoredProjectChanges = (
  storage?: ProjectChangesStorage
): StoredProjectChanges => {
  let target = storage;
  try {
    target ??= globalThis.localStorage;
    const raw = target.getItem(storageKey);
    if (!raw) {
      return emptyStoredProjectChanges();
    }
    const parsed = storedProjectChangesSchema.safeParse(JSON.parse(raw));
    if (parsed.success) {
      return {
        changes: parsed.data.changes,
        resourceDrafts: parsed.data.resourceDrafts,
      };
    }
  } catch {
    // localStorage can be unavailable or contain malformed data.
  }
  try {
    target?.removeItem(storageKey);
  } catch {
    // A disabled localStorage can reject cleanup as well.
  }
  return emptyStoredProjectChanges();
};

export const saveStoredProjectChanges = (
  value: StoredProjectChanges,
  storage?: ProjectChangesStorage
) => {
  try {
    const target = storage ?? globalThis.localStorage;
    if (
      Object.keys(value.changes).length === 0 &&
      Object.keys(value.resourceDrafts).length === 0
    ) {
      target.removeItem(storageKey);
      return;
    }
    target.setItem(storageKey, JSON.stringify({ ...value, version: 1 }));
  } catch {
    // Persistence is best effort when storage is disabled or full.
  }
};
