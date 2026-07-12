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

export type LogRecord = z.infer<typeof logRecordSchema>;
export type LogWindow = z.infer<typeof logWindowSchema>;

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
