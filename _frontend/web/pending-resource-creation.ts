import {
  createManagedPostgres,
  createManagedRedis,
  createObjectStore,
  createService,
} from "@/api";
import type {
  CreateManagedPostgresInput,
  CreateManagedRedisInput,
  CreateObjectStoreInput,
  CreateServiceInput,
  ProjectCanvas,
} from "@/api";

export type PendingResourceCreation =
  | {
      id: string;
      input: CreateManagedPostgresInput;
      kind: "postgres";
    }
  | { id: string; input: CreateManagedRedisInput; kind: "redis" }
  | { id: string; input: CreateObjectStoreInput; kind: "storage" }
  | { id: string; input: CreateServiceInput; kind: "service" };

export const newResourceDraftID = () => `draft:${crypto.randomUUID()}`;

const resourceLabels: Record<PendingResourceCreation["kind"], string> = {
  postgres: "PostgreSQL",
  redis: "Redis",
  service: "Service",
  storage: "Object storage",
};

export const pendingResourceLabel = (draft: PendingResourceCreation) =>
  resourceLabels[draft.kind];

export const pendingResourceName = (draft: PendingResourceCreation) =>
  draft.input.name;

export const applyPendingResource = (
  projectID: string,
  draft: PendingResourceCreation
) => {
  switch (draft.kind) {
    case "postgres": {
      return createManagedPostgres(projectID, draft.input);
    }
    case "redis": {
      return createManagedRedis(projectID, draft.input);
    }
    case "service": {
      return createService(projectID, draft.input);
    }
    case "storage": {
      return createObjectStore(projectID, draft.input);
    }
    default: {
      throw new Error("Unsupported resource draft");
    }
  }
};

export const pendingCanvasResource = (
  draft: PendingResourceCreation,
  projectName: string
): ProjectCanvas["resources"][number] => {
  const common = {
    enabled: true,
    id: draft.id,
    internalHostname: `${draft.input.name}.${projectName}.internal`,
    name: draft.input.name,
    status: "pending" as const,
    statusMessage: "Local draft · deploy to create",
    volumes: [],
  };
  switch (draft.kind) {
    case "postgres": {
      return {
        ...common,
        imageReference: `postgres:${draft.input.imageTag}`,
        kind: "postgres",
      };
    }
    case "redis": {
      return {
        ...common,
        imageReference: `redis:${draft.input.imageTag}`,
        kind: "redis",
      };
    }
    case "service": {
      return { ...common, kind: "service", source: draft.input.source };
    }
    case "storage": {
      return {
        ...common,
        bucketName: draft.input.bucketName,
        kind: "object_store",
      };
    }
    default: {
      throw new Error("Unsupported resource draft");
    }
  }
};
