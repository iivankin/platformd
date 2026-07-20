import type { ResourceNodeData } from "@/project-flow";

export type ResourceCollection =
  | "network-gateways"
  | "object-stores"
  | "postgres"
  | "redis"
  | "services";

export type DeploymentWorkspaceView = "build-logs" | "deploy-logs" | "details";

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

export const resourceDeploymentPath = (
  projectID: string,
  resourceID: string,
  kind: "postgres" | "redis" | "service",
  deploymentID: string,
  view: DeploymentWorkspaceView = "deploy-logs"
) =>
  `/projects/${encodeURIComponent(projectID)}/${collectionByKind[kind]}/${encodeURIComponent(resourceID)}/deployments/${encodeURIComponent(deploymentID)}/${view}`;

export const deploymentPath = (
  projectID: string,
  serviceID: string,
  deploymentID: string,
  view: DeploymentWorkspaceView = "deploy-logs"
) =>
  resourceDeploymentPath(projectID, serviceID, "service", deploymentID, view);
