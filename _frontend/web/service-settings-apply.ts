import {
  attachServiceDomain,
  attachServiceListener,
  createVolume,
  detachServiceDomain,
  detachServiceListener,
  fetchServiceDomains,
  fetchServiceListeners,
  fetchVolumes,
  updateService,
} from "@/api";
import type { Service } from "@/api";
import { parseServiceConfiguration } from "@/service-configuration";
import { serviceListenerDraftKey } from "@/service-settings-model";
import type { PendingServiceSettings } from "@/service-settings-model";

export const applyServiceSettings = async (
  projectID: string,
  change: PendingServiceSettings
): Promise<Service> => {
  const configuration = parseServiceConfiguration(
    change.draft.configuration,
    change.draft.domains.length
  );
  const [currentDomains, currentListeners, currentVolumes] = await Promise.all([
    fetchServiceDomains(projectID, change.serviceID),
    fetchServiceListeners(projectID, change.serviceID),
    fetchVolumes(projectID, change.serviceID),
  ]);
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
    command: service.command,
    cpuMillicores: service.cpuMillicores,
    enabled: service.enabled,
    environment: service.environment,
    expectedUpdatedAt: service.updatedAt,
    healthCheck: configuration.healthCheck,
    memoryMaxBytes: service.memoryMaxBytes,
    registryCredential: configuration.registryCredential,
    secretReferences: service.secretReferences,
    source: configuration.source,
    volumeMounts: change.draft.volumeMounts.map((mount) => ({
      ...mount,
      volumeId: createdVolumeIDs.get(mount.volumeId) ?? mount.volumeId,
    })),
  });
};
