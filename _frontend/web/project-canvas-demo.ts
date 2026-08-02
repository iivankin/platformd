import type { ProjectCanvas } from "@/api";

export const demoCanvasPresets = [
  { group: "current", label: "Default", value: "default" },
  { group: "examples", label: "Web app", value: "web-app" },
  { group: "examples", label: "SaaS", value: "saas" },
  {
    group: "examples",
    label: "Microservices",
    value: "microservices",
  },
  { group: "examples", label: "Pipeline", value: "data-pipeline" },
  { group: "stress", label: "Fan-out", value: "fan-out" },
  { group: "stress", label: "Diamond", value: "diamond" },
  { group: "stress", label: "Dense", value: "dense" },
] as const;

export type DemoCanvasPreset = (typeof demoCanvasPresets)[number]["value"];

type CanvasResource = ProjectCanvas["resources"][number];
type ResourceKind = CanvasResource["kind"];

interface DemoResourceDefinition {
  id: string;
  kind: ResourceKind;
  name: string;
}

interface DemoPresetDefinition {
  connections: [sourceID: string, targetID: string][];
  resources: DemoResourceDefinition[];
}

const webApp: DemoPresetDefinition = {
  connections: [
    ["demo-web", "demo-api"],
    ["demo-api", "demo-primary"],
    ["demo-api", "demo-cache"],
    ["demo-api", "demo-assets"],
    ["demo-worker", "demo-primary"],
    ["demo-worker", "demo-cache"],
    ["demo-worker", "demo-assets"],
  ],
  resources: [
    { id: "demo-web", kind: "service", name: "web" },
    { id: "demo-api", kind: "service", name: "api" },
    { id: "demo-worker", kind: "service", name: "worker" },
    { id: "demo-primary", kind: "postgres", name: "primary" },
    { id: "demo-cache", kind: "redis", name: "cache" },
    { id: "demo-assets", kind: "object_store", name: "uploads" },
  ],
};

const saas: DemoPresetDefinition = {
  connections: [
    ["demo-dashboard", "demo-api"],
    ["demo-api", "demo-primary"],
    ["demo-api", "demo-cache"],
    ["demo-api", "demo-files"],
    ["demo-api", "demo-mailer"],
    ["demo-worker", "demo-primary"],
    ["demo-worker", "demo-cache"],
    ["demo-worker", "demo-files"],
    ["demo-worker", "demo-mailer"],
    ["demo-scheduler", "demo-worker"],
  ],
  resources: [
    { id: "demo-dashboard", kind: "service", name: "dashboard" },
    { id: "demo-api", kind: "service", name: "api" },
    { id: "demo-worker", kind: "service", name: "worker" },
    { id: "demo-scheduler", kind: "service", name: "scheduler" },
    { id: "demo-mailer", kind: "service", name: "mailer" },
    { id: "demo-primary", kind: "postgres", name: "primary" },
    { id: "demo-cache", kind: "redis", name: "cache" },
    { id: "demo-files", kind: "object_store", name: "customer-files" },
  ],
};

const microservices: DemoPresetDefinition = {
  connections: [
    ["demo-gateway", "demo-identity"],
    ["demo-gateway", "demo-catalog"],
    ["demo-gateway", "demo-checkout"],
    ["demo-identity", "demo-accounts"],
    ["demo-identity", "demo-cache"],
    ["demo-catalog", "demo-inventory"],
    ["demo-catalog", "demo-cache"],
    ["demo-checkout", "demo-accounts"],
    ["demo-checkout", "demo-inventory"],
    ["demo-checkout", "demo-orders"],
    ["demo-checkout", "demo-cache"],
    ["demo-worker", "demo-orders"],
    ["demo-worker", "demo-cache"],
  ],
  resources: [
    { id: "demo-gateway", kind: "service", name: "gateway" },
    { id: "demo-identity", kind: "service", name: "identity" },
    { id: "demo-catalog", kind: "service", name: "catalog" },
    { id: "demo-checkout", kind: "service", name: "checkout" },
    { id: "demo-worker", kind: "service", name: "orders-worker" },
    { id: "demo-accounts", kind: "postgres", name: "accounts" },
    { id: "demo-inventory", kind: "postgres", name: "inventory" },
    { id: "demo-orders", kind: "postgres", name: "orders" },
    { id: "demo-cache", kind: "redis", name: "shared-cache" },
  ],
};

const dataPipeline: DemoPresetDefinition = {
  connections: [
    ["demo-dashboard", "demo-analytics"],
    ["demo-analytics", "demo-warehouse"],
    ["demo-collector", "demo-raw"],
    ["demo-collector", "demo-buffer"],
    ["demo-transformer", "demo-raw"],
    ["demo-transformer", "demo-warehouse"],
    ["demo-transformer", "demo-buffer"],
    ["demo-exporter", "demo-warehouse"],
    ["demo-exporter", "demo-reports"],
    ["demo-scheduler", "demo-transformer"],
  ],
  resources: [
    { id: "demo-dashboard", kind: "service", name: "dashboard" },
    { id: "demo-analytics", kind: "service", name: "analytics" },
    { id: "demo-collector", kind: "service", name: "collector" },
    { id: "demo-transformer", kind: "service", name: "transformer" },
    { id: "demo-exporter", kind: "service", name: "exporter" },
    { id: "demo-scheduler", kind: "service", name: "scheduler" },
    { id: "demo-raw", kind: "object_store", name: "raw-events" },
    { id: "demo-warehouse", kind: "postgres", name: "warehouse" },
    { id: "demo-buffer", kind: "redis", name: "event-buffer" },
    { id: "demo-reports", kind: "object_store", name: "reports" },
  ],
};

const fanOut: DemoPresetDefinition = {
  connections: [
    ["demo-api", "demo-primary"],
    ["demo-api", "demo-cache"],
    ["demo-api", "demo-assets"],
    ["demo-api", "demo-search"],
    ["demo-api", "demo-mailer"],
    ["demo-api", "demo-analytics"],
  ],
  resources: [
    { id: "demo-api", kind: "service", name: "api" },
    { id: "demo-primary", kind: "postgres", name: "primary" },
    { id: "demo-cache", kind: "redis", name: "cache" },
    { id: "demo-assets", kind: "object_store", name: "assets" },
    { id: "demo-search", kind: "service", name: "search" },
    { id: "demo-mailer", kind: "service", name: "mailer" },
    { id: "demo-analytics", kind: "postgres", name: "analytics" },
  ],
};

const diamond: DemoPresetDefinition = {
  connections: [
    ["demo-web", "demo-api"],
    ["demo-web", "demo-worker"],
    ["demo-api", "demo-primary"],
    ["demo-worker", "demo-primary"],
  ],
  resources: [
    { id: "demo-web", kind: "service", name: "web" },
    { id: "demo-api", kind: "service", name: "api" },
    { id: "demo-worker", kind: "service", name: "worker" },
    { id: "demo-primary", kind: "postgres", name: "primary" },
  ],
};

const dense: DemoPresetDefinition = {
  connections: [
    ["demo-web", "demo-api"],
    ["demo-web", "demo-jobs"],
    ["demo-worker", "demo-api"],
    ["demo-worker", "demo-jobs"],
    ["demo-worker", "demo-events"],
    ["demo-scheduler", "demo-jobs"],
    ["demo-scheduler", "demo-events"],
    ["demo-api", "demo-primary"],
    ["demo-api", "demo-cache"],
    ["demo-jobs", "demo-primary"],
    ["demo-jobs", "demo-cache"],
    ["demo-jobs", "demo-assets"],
    ["demo-events", "demo-cache"],
    ["demo-events", "demo-assets"],
  ],
  resources: [
    { id: "demo-web", kind: "service", name: "web" },
    { id: "demo-worker", kind: "service", name: "worker" },
    { id: "demo-scheduler", kind: "service", name: "scheduler" },
    { id: "demo-api", kind: "service", name: "api" },
    { id: "demo-jobs", kind: "service", name: "jobs" },
    { id: "demo-events", kind: "service", name: "events" },
    { id: "demo-primary", kind: "postgres", name: "primary" },
    { id: "demo-cache", kind: "redis", name: "cache" },
    { id: "demo-assets", kind: "object_store", name: "assets" },
  ],
};

const definitions: Record<
  Exclude<DemoCanvasPreset, "default">,
  DemoPresetDefinition
> = {
  "data-pipeline": dataPipeline,
  dense,
  diamond,
  "fan-out": fanOut,
  microservices,
  saas,
  "web-app": webApp,
};

const imageReference = (
  kind: ResourceKind,
  name: string
): string | undefined => {
  if (kind === "service") {
    return `registry.mock.local/demo/${name}:latest`;
  }
  if (kind === "postgres") {
    return "postgres:17.5";
  }
  if (kind === "redis") {
    return "redis:8.2";
  }
};

const demoResource = (
  definition: DemoResourceDefinition,
  projectName: string
): CanvasResource => ({
  bucketName: definition.kind === "object_store" ? definition.name : undefined,
  enabled: true,
  id: definition.id,
  imageReference: imageReference(definition.kind, definition.name),
  internalHostname: `${definition.name}.${projectName}.internal`,
  kind: definition.kind,
  name: definition.name,
  status: "running",
  volumes: [],
});

const resourceCount = (
  resources: CanvasResource[],
  kind: ResourceKind
): number => resources.filter((resource) => resource.kind === kind).length;

export const projectCanvasForDemoPreset = (
  canvas: ProjectCanvas,
  preset: DemoCanvasPreset
): ProjectCanvas => {
  if (preset === "default") {
    return canvas;
  }
  const definition = definitions[preset];
  const resources = definition.resources.map((resource) =>
    demoResource(resource, canvas.project.name)
  );
  return {
    connections: definition.connections.map(([sourceId, targetId], index) => ({
      environmentNames: [`DEMO_REFERENCE_${index + 1}`],
      sourceId,
      targetId,
    })),
    project: {
      ...canvas.project,
      networkGatewayCount: resourceCount(resources, "network_gateway"),
      objectStoreCount: resourceCount(resources, "object_store"),
      postgresCount: resourceCount(resources, "postgres"),
      redisCount: resourceCount(resources, "redis"),
      serviceCount: resourceCount(resources, "service"),
    },
    resources,
  };
};
