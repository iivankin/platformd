import type { ResourceNodeData } from "@/project-flow";

export type ResourceCollection =
  | "network-gateways"
  | "object-stores"
  | "postgres"
  | "redis"
  | "services";

const collectionByKind: Record<ResourceNodeData["kind"], ResourceCollection> = {
  network_gateway: "network-gateways",
  object_store: "object-stores",
  postgres: "postgres",
  redis: "redis",
  service: "services",
};

const defaultViewByKind: Record<ResourceNodeData["kind"], string> = {
  network_gateway: "variables",
  object_store: "objects",
  postgres: "deployments",
  redis: "deployments",
  service: "deployments",
};

export const resourceCollection = (kind: ResourceNodeData["kind"]) =>
  collectionByKind[kind];

export const resourcePath = (
  projectID: string,
  resourceID: string,
  kind: ResourceNodeData["kind"],
  view?: string
) =>
  `/projects/${encodeURIComponent(projectID)}/${collectionByKind[kind]}/${encodeURIComponent(resourceID)}/${view ?? defaultViewByKind[kind]}`;

export const resourceKind = (
  collection: string
): ResourceNodeData["kind"] | undefined => {
  const entry = Object.entries(collectionByKind).find(
    ([, value]) => value === collection
  );
  return entry?.[0] as ResourceNodeData["kind"] | undefined;
};

export const resourceTelemetryLogsPath = (
  projectID: string,
  resourceID: string,
  kind: "object_store" | "postgres" | "redis" | "service",
  deploymentID?: string
) => {
  const path = resourcePath(projectID, resourceID, kind, "telemetry");
  const query = new URLSearchParams({ telemetry: "logs" });
  if (deploymentID) {
    query.set("deployment", deploymentID);
  }
  return `${path}?${query.toString()}`;
};
