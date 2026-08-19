import type { Service, ServiceDomain, ServiceListener, Volume } from "@/api";
import { beforeDeployDraft } from "@/service-before-deploy-model";
import type { BeforeDeployDraft } from "@/service-before-deploy-model";
import { serviceConfigurationDraft } from "@/service-configuration";
import type { ServiceConfigurationDraft } from "@/service-configuration";
import { portForwardDraft } from "@/service-port-forward";
import type { PortForwardDraft } from "@/service-port-forward";

export type ServiceDomainDraft = Pick<ServiceDomain, "hostname" | "targetPort">;
export type ServiceListenerDraft = Pick<
  ServiceListener,
  "protocol" | "publicPort" | "targetPort"
>;
export type ServiceVolumeDraft = Volume & { pendingCreation?: boolean };

export interface ServiceSettingsDraft {
  beforeDeploy: BeforeDeployDraft;
  configuration: ServiceConfigurationDraft;
  domains: ServiceDomainDraft[];
  hostId: string;
  listeners: ServiceListenerDraft[];
  portForward: PortForwardDraft;
  volumeMounts: Service["volumeMounts"];
  volumes: ServiceVolumeDraft[];
}

export interface PendingServiceSettings {
  baseline: {
    domains: ServiceDomainDraft[];
    listeners: ServiceListenerDraft[];
    service: Service;
    volumes: Volume[];
  };
  draft: ServiceSettingsDraft;
  environment: Record<string, string>;
  serviceID: string;
  serviceName: string;
}

export interface ServiceSettingsChangeDetail {
  detail: string;
  id: string;
  label: string;
}

const domainDrafts = (domains: ServiceDomain[]): ServiceDomainDraft[] =>
  domains
    .map(({ hostname, targetPort }) => ({ hostname, targetPort }))
    .toSorted((left, right) => left.hostname.localeCompare(right.hostname));

const listenerKey = (
  listener: Pick<ServiceListenerDraft, "protocol" | "publicPort">
) => `${listener.protocol}:${listener.publicPort}`;

const listenerDrafts = (listeners: ServiceListener[]): ServiceListenerDraft[] =>
  listeners
    .map(({ protocol, publicPort, targetPort }) => ({
      protocol,
      publicPort,
      targetPort,
    }))
    .toSorted(
      (left, right) =>
        left.publicPort - right.publicPort ||
        left.protocol.localeCompare(right.protocol)
    );

const sortedMounts = (mounts: Service["volumeMounts"]) =>
  mounts.toSorted(
    (left, right) =>
      left.volumeId.localeCompare(right.volumeId) ||
      left.containerPath.localeCompare(right.containerPath)
  );

const sortedVolumes = <Item extends Volume>(volumes: Item[]): Item[] =>
  volumes.toSorted(
    (left, right) =>
      left.name.localeCompare(right.name) || left.id.localeCompare(right.id)
  );

const same = (left: unknown, right: unknown) =>
  JSON.stringify(left) === JSON.stringify(right);

const sourceDescription = (source: Service["source"]) => {
  if (source.type === "docker_image_upload") {
    return `${source.dockerUpload.repository} · ${source.dockerUpload.branch}`;
  }
  if (source.type === "unconfigured") {
    return "Not configured";
  }
  return source.image.reference;
};

const sortedEnvironment = (environment: Record<string, string>) =>
  Object.fromEntries(
    Object.entries(environment).toSorted(([left], [right]) =>
      left.localeCompare(right)
    )
  );

export const createServiceSettingsDraft = (
  service: Service,
  domains: ServiceDomain[],
  listeners: ServiceListener[],
  volumes: Volume[]
): ServiceSettingsDraft => ({
  beforeDeploy: beforeDeployDraft(service.beforeDeploy),
  configuration: serviceConfigurationDraft(service),
  domains: domainDrafts(domains),
  hostId: service.hostId ?? "",
  listeners: listenerDrafts(listeners),
  portForward: portForwardDraft(service.portForward),
  volumeMounts: sortedMounts(service.volumeMounts),
  volumes: sortedVolumes(volumes),
});

const configurationChangeDetails = (
  change: PendingServiceSettings
): ServiceSettingsChangeDetail[] => {
  const details: ServiceSettingsChangeDetail[] = [];
  if ((change.baseline.service.hostId ?? "") !== change.draft.hostId) {
    details.push({
      detail: change.draft.hostId === "" ? "Primary VPS" : change.draft.hostId,
      id: "host",
      label: "Server",
    });
  }
  const baselineConfiguration = serviceConfigurationDraft(
    change.baseline.service
  );
  if (!same(baselineConfiguration.source, change.draft.configuration.source)) {
    const { source } = change.draft.configuration;
    details.push({
      detail: sourceDescription(source),
      id: "source",
      label: "Source",
    });
  }
  if (
    change.draft.configuration.source.type === "private_image" &&
    !same(
      baselineConfiguration.registryCredential,
      change.draft.configuration.registryCredential
    )
  ) {
    details.push({
      detail: change.draft.configuration.registryCredential.username,
      id: "registry-credential",
      label: "Registry access",
    });
  }
  if (
    !same(
      {
        enabled: baselineConfiguration.healthEnabled,
        path: baselineConfiguration.healthPath,
        port: baselineConfiguration.healthPort,
        timeout: baselineConfiguration.healthTimeout,
      },
      {
        enabled: change.draft.configuration.healthEnabled,
        path: change.draft.configuration.healthPath,
        port: change.draft.configuration.healthPort,
        timeout: change.draft.configuration.healthTimeout,
      }
    )
  ) {
    details.push({
      detail: change.draft.configuration.healthEnabled
        ? `${change.draft.configuration.healthPath} on :${change.draft.configuration.healthPort}`
        : "Off",
      id: "health",
      label: "Health check",
    });
  }

  return details;
};

const beforeDeployChangeDetails = (
  change: PendingServiceSettings
): ServiceSettingsChangeDetail[] => {
  const baseline = beforeDeployDraft(change.baseline.service.beforeDeploy);
  const current = change.draft.beforeDeploy;
  const details: ServiceSettingsChangeDetail[] = [];
  if (
    !same(
      { command: baseline.command, enabled: baseline.commandEnabled },
      { command: current.command, enabled: current.commandEnabled }
    )
  ) {
    details.push({
      detail: current.commandEnabled ? current.command.trim() : "Off",
      id: "before-deploy-command",
      label: "Before deploy · command",
    });
  }
  if (
    !same(
      {
        enabled: baseline.cloudflareEnabled,
        hostnames: baseline.cloudflareHostnames.toSorted(),
      },
      {
        enabled: current.cloudflareEnabled,
        hostnames: current.cloudflareHostnames.toSorted(),
      }
    )
  ) {
    details.push({
      detail: current.cloudflareEnabled
        ? current.cloudflareHostnames.join(", ") || "Not selected"
        : "Off",
      id: "before-deploy-cloudflare",
      label: "Before deploy · Cloudflare purge",
    });
  }
  return details;
};

const portForwardChangeDetails = (
  change: PendingServiceSettings
): ServiceSettingsChangeDetail[] => {
  const baseline = portForwardDraft(change.baseline.service.portForward);
  const current = change.draft.portForward;
  if (same(baseline, current)) {
    return [];
  }
  const detail = current.repository.trim()
    ? `${current.repository}${
        current.workflows.length ? ` · ${current.workflows.join(", ")}` : ""
      }`
    : "Disabled";
  return [
    {
      detail,
      id: "port-forward",
      label: "GitHub Actions port forward",
    },
  ];
};

const variableChangeDetails = (
  baseline: Readonly<Record<string, string>>,
  current: Readonly<Record<string, string>>
): ServiceSettingsChangeDetail[] => {
  const details: ServiceSettingsChangeDetail[] = [];
  for (const [name, value] of Object.entries(current)) {
    if (baseline[name] !== value) {
      const exists = Object.hasOwn(baseline, name);
      details.push({
        detail: name,
        id: `variable:${name}`,
        label: exists ? "Update variable" : "Add variable",
      });
    }
  }
  for (const name of Object.keys(baseline)) {
    if (!Object.hasOwn(current, name)) {
      details.push({
        detail: name,
        id: `variable:${name}`,
        label: "Remove variable",
      });
    }
  }
  return details.toSorted((left, right) =>
    left.detail.localeCompare(right.detail)
  );
};

const domainChangeDetails = (
  change: PendingServiceSettings
): ServiceSettingsChangeDetail[] => {
  const details: ServiceSettingsChangeDetail[] = [];
  const baselineDomains = new Map(
    change.baseline.domains.map((domain) => [domain.hostname, domain])
  );
  const draftDomains = new Map(
    change.draft.domains.map((domain) => [domain.hostname, domain])
  );
  for (const domain of change.draft.domains) {
    const baseline = baselineDomains.get(domain.hostname);
    if (!baseline || baseline.targetPort !== domain.targetPort) {
      details.push({
        detail: `${domain.hostname} → :${domain.targetPort}`,
        id: `domain:${domain.hostname}`,
        label: baseline ? "Domain port" : "Add domain",
      });
    }
  }
  for (const domain of change.baseline.domains) {
    if (!draftDomains.has(domain.hostname)) {
      details.push({
        detail: domain.hostname,
        id: `domain:${domain.hostname}`,
        label: "Remove domain",
      });
    }
  }

  return details;
};

const listenerChangeDetails = (
  change: PendingServiceSettings
): ServiceSettingsChangeDetail[] => {
  const details: ServiceSettingsChangeDetail[] = [];
  const baselineListeners = new Map(
    change.baseline.listeners.map((listener) => [
      listenerKey(listener),
      listener,
    ])
  );
  const draftListeners = new Map(
    change.draft.listeners.map((listener) => [listenerKey(listener), listener])
  );
  for (const listener of change.draft.listeners) {
    const key = listenerKey(listener);
    const baseline = baselineListeners.get(key);
    if (!baseline || baseline.targetPort !== listener.targetPort) {
      details.push({
        detail: `${listener.protocol.toUpperCase()} :${listener.publicPort} → :${listener.targetPort}`,
        id: `listener:${key}`,
        label: baseline ? "Listener target" : "Add listener",
      });
    }
  }
  for (const listener of change.baseline.listeners) {
    const key = listenerKey(listener);
    if (!draftListeners.has(key)) {
      details.push({
        detail: `${listener.protocol.toUpperCase()} :${listener.publicPort}`,
        id: `listener:${key}`,
        label: "Remove listener",
      });
    }
  }

  return details;
};

const volumeChangeDetails = (
  change: PendingServiceSettings
): ServiceSettingsChangeDetail[] => {
  const details: ServiceSettingsChangeDetail[] = [];
  const baselineVolumeIDs = new Set(
    change.baseline.volumes.map((volume) => volume.id)
  );
  const addedVolumes = change.draft.volumes.filter(
    (volume) => !baselineVolumeIDs.has(volume.id)
  );
  const addedVolumeIDs = new Set(addedVolumes.map((volume) => volume.id));
  const draftMountsByVolume = new Map(
    change.draft.volumeMounts.map((mount) => [
      mount.volumeId,
      mount.containerPath,
    ])
  );
  for (const volume of addedVolumes) {
    const mountPath = draftMountsByVolume.get(volume.id);
    details.push({
      detail: mountPath ? `${volume.name} → ${mountPath}` : volume.name,
      id: `volume:create:${volume.id}`,
      label: "Add volume",
    });
  }
  const baselineMounts = new Map(
    change.baseline.service.volumeMounts.map((mount) => [
      mount.volumeId,
      mount.containerPath,
    ])
  );
  const draftMounts = new Map(
    change.draft.volumeMounts.map((mount) => [
      mount.volumeId,
      mount.containerPath,
    ])
  );
  const volumeIDs = new Set([...baselineMounts.keys(), ...draftMounts.keys()]);
  for (const volumeID of volumeIDs) {
    if (addedVolumeIDs.has(volumeID)) {
      continue;
    }
    const before = baselineMounts.get(volumeID);
    const after = draftMounts.get(volumeID);
    if (before !== after) {
      details.push({
        detail: after ? `${volumeID} → ${after}` : volumeID,
        id: `volume:${volumeID}`,
        label: after ? "Mount volume" : "Unmount volume",
      });
    }
  }
  return details;
};

export const serviceSettingsChangeDetails = (
  change: PendingServiceSettings
): ServiceSettingsChangeDetail[] => [
  ...variableChangeDetails(
    change.baseline.service.environment,
    change.environment
  ),
  ...configurationChangeDetails(change),
  ...beforeDeployChangeDetails(change),
  ...portForwardChangeDetails(change),
  ...domainChangeDetails(change),
  ...listenerChangeDetails(change),
  ...volumeChangeDetails(change),
];

export const createPendingServiceSettings = ({
  current,
  domains,
  draft,
  environment,
  listeners,
  service,
  volumes,
}: {
  current?: PendingServiceSettings;
  domains: ServiceDomain[];
  draft: ServiceSettingsDraft;
  environment?: Record<string, string>;
  listeners: ServiceListener[];
  service: Service;
  volumes: Volume[];
}): PendingServiceSettings | undefined => {
  const change: PendingServiceSettings = {
    baseline: current?.baseline ?? {
      domains: domainDrafts(domains),
      listeners: listenerDrafts(listeners),
      service,
      volumes: sortedVolumes(volumes),
    },
    draft: {
      ...draft,
      domains: draft.domains.toSorted((left, right) =>
        left.hostname.localeCompare(right.hostname)
      ),
      listeners: draft.listeners.toSorted(
        (left, right) =>
          left.publicPort - right.publicPort ||
          left.protocol.localeCompare(right.protocol)
      ),
      volumeMounts: sortedMounts(draft.volumeMounts),
      volumes: sortedVolumes(draft.volumes),
    },
    environment: sortedEnvironment(
      environment ?? current?.environment ?? service.environment
    ),
    serviceID: service.id,
    serviceName: service.name,
  };
  return serviceSettingsChangeDetails(change).length ? change : undefined;
};

export const serviceListenerDraftKey = listenerKey;
