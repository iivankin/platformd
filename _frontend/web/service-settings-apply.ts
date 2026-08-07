import {
  attachServiceDomain,
  attachServiceListener,
  createVolume,
  detachServiceDomain,
  detachServiceListener,
  fetchService,
  fetchServiceDomains,
  fetchServiceListeners,
  fetchVolumes,
  updateService,
} from "@/api";
import type { Service, ServiceDomain, ServiceListener, Volume } from "@/api";
import { parseBeforeDeploy } from "@/service-before-deploy-model";
import { parseServiceConfiguration } from "@/service-configuration";
import { parsePortForward } from "@/service-port-forward";
import { serviceListenerDraftKey } from "@/service-settings-model";
import type { PendingServiceSettings } from "@/service-settings-model";

const comparableDomains = (
  domains: readonly Pick<ServiceDomain, "hostname" | "targetPort">[]
) =>
  domains
    .map(({ hostname, targetPort }) => ({ hostname, targetPort }))
    .toSorted((left, right) => left.hostname.localeCompare(right.hostname));

const comparableListeners = (
  listeners: readonly Pick<
    ServiceListener,
    "protocol" | "publicPort" | "targetPort"
  >[]
) =>
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

const same = (left: unknown, right: unknown) =>
  JSON.stringify(left) === JSON.stringify(right);

export const assertServiceSettingsBaseline = (
  change: PendingServiceSettings,
  currentService: Service,
  currentDomains: ServiceDomain[],
  currentListeners: ServiceListener[],
  currentVolumes: Volume[]
) => {
  const { baseline } = change;
  const settingsChanged =
    currentService.updatedAt !== baseline.service.updatedAt ||
    !same(comparableDomains(currentDomains), baseline.domains) ||
    !same(comparableListeners(currentListeners), baseline.listeners);
  const currentVolumesByID = new Map(
    currentVolumes.map((volume) => [volume.id, volume])
  );
  // Extra volumes are safe: a previous attempt may have created one before a
  // later mutation failed, and applyServiceSettings reuses it by name.
  const baselineVolumesExist = baseline.volumes.every((volume) => {
    const current = currentVolumesByID.get(volume.id);
    return current?.name === volume.name;
  });
  if (settingsChanged || !baselineVolumesExist) {
    throw new Error(
      "Service settings changed after this draft was staged. Review the current configuration and stage the change again."
    );
  }
};

export const applyServiceSettings = async (
  projectID: string,
  change: PendingServiceSettings
): Promise<Service> => {
  const configuration = parseServiceConfiguration(change.draft.configuration);
  const beforeDeploy = parseBeforeDeploy(
    change.draft.beforeDeploy,
    change.draft.domains
  );
  const [currentService, currentDomains, currentListeners, currentVolumes] =
    await Promise.all([
      fetchService(projectID, change.serviceID),
      fetchServiceDomains(projectID, change.serviceID),
      fetchServiceListeners(projectID, change.serviceID),
      fetchVolumes(projectID, change.serviceID),
    ]);
  assertServiceSettingsBaseline(
    change,
    currentService,
    currentDomains,
    currentListeners,
    currentVolumes
  );
  const baselineVolumeIDs = new Set(
    change.baseline.volumes.map((volume) => volume.id)
  );
  const currentVolumesByName = new Map(
    currentVolumes.map((volume) => [volume.name, volume])
  );
  const createdVolumeIDs = new Map<string, string>();
  await Promise.all(
    change.draft.volumes
      .filter((volume) => !baselineVolumeIDs.has(volume.id))
      .map(async (volume) => {
        const existing = currentVolumesByName.get(volume.name);
        const created =
          existing ??
          (await createVolume(projectID, change.serviceID, {
            name: volume.name,
          }));
        createdVolumeIDs.set(volume.id, created.id);
      })
  );
  const currentDomainsByHostname = new Map(
    currentDomains.map((domain) => [domain.hostname, domain])
  );
  const draftDomains = new Map(
    change.draft.domains.map((domain) => [domain.hostname, domain])
  );
  const currentListenersByKey = new Map(
    currentListeners.map((listener) => [
      serviceListenerDraftKey(listener),
      listener,
    ])
  );
  const draftListeners = new Map(
    change.draft.listeners.map((listener) => [
      serviceListenerDraftKey(listener),
      listener,
    ])
  );

  const removals: Promise<unknown>[] = [];
  for (const domain of currentDomains) {
    if (!draftDomains.has(domain.hostname)) {
      removals.push(
        detachServiceDomain(projectID, change.serviceID, domain.hostname)
      );
    }
  }
  for (const listener of currentListeners) {
    if (!draftListeners.has(serviceListenerDraftKey(listener))) {
      removals.push(
        detachServiceListener(
          projectID,
          change.serviceID,
          listener.protocol,
          listener.publicPort
        )
      );
    }
  }
  await Promise.all(removals);

  const additions: Promise<unknown>[] = [];
  for (const domain of change.draft.domains) {
    const current = currentDomainsByHostname.get(domain.hostname);
    if (!current || current.targetPort !== domain.targetPort) {
      additions.push(
        attachServiceDomain(
          projectID,
          change.serviceID,
          domain.hostname,
          domain.targetPort
        )
      );
    }
  }
  for (const listener of change.draft.listeners) {
    const current = currentListenersByKey.get(
      serviceListenerDraftKey(listener)
    );
    if (!current || current.targetPort !== listener.targetPort) {
      additions.push(
        attachServiceListener(projectID, change.serviceID, listener)
      );
    }
  }
  await Promise.all(additions);

  const { service } = change.baseline;
  return updateService(projectID, change.serviceID, {
    args: service.args,
    beforeDeploy,
    command: service.command,
    cpuMillicores: service.cpuMillicores,
    enabled: service.enabled,
    environment: change.environment,
    expectedUpdatedAt: service.updatedAt,
    healthCheck: configuration.healthCheck,
    memoryMaxBytes: service.memoryMaxBytes,
    portForward: parsePortForward(change.draft.portForward),
    registryCredential: configuration.registryCredential,
    secretReferences: service.secretReferences,
    source: configuration.source,
    volumeMounts: change.draft.volumeMounts.map((mount) => ({
      ...mount,
      volumeId: createdVolumeIDs.get(mount.volumeId) ?? mount.volumeId,
    })),
  });
};
