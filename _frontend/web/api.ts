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
const apiErrorSchema = z.object({
  error: z.object({ code: z.string(), message: z.string() }),
});

export type Project = z.infer<typeof projectSchema>;

const canvasResourceSchema = z.object({
  bucketName: z.string().optional(),
  enabled: z.boolean(),
  id: z.string().min(1),
  imageReference: z.string().optional(),
  internalHostname: z.string().min(1),
  kind: z.enum(["service", "postgres", "redis", "object_store"]),
  name: z.string().min(1),
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
  activeDeploymentId: z.string().min(1).optional(),
  args: z.array(z.string()).optional(),
  command: z.array(z.string()).optional(),
  cpuMillicores: z.number().int().nonnegative().optional(),
  enabled: z.boolean(),
  environment: z.record(z.string(), z.string()),
  healthPath: z.string().optional(),
  id: z.string().min(1),
  imageCredentialId: z.string().min(1).optional(),
  imageReference: z.string().min(1),
  memoryMaxBytes: z.number().int().nonnegative().optional(),
  name: z.string().min(1),
  projectId: z.string().min(1),
  startupTimeoutSeconds: z.number().int().positive(),
  targetPort: z.number().int().min(1).max(65_535).optional(),
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

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit
) => Promise<Response>;

const apiError = async (response: Response, fallback: string) => {
  const parsed = apiErrorSchema.safeParse(
    await response.json().catch(() => null)
  );
  return new Error(parsed.success ? parsed.data.error.message : fallback);
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
