import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";

import {
  attachServiceDomain,
  attachServiceListener,
  deleteService,
  deployServiceVersion,
  fetchImageCredentials,
  fetchRegistrySettings,
  fetchService,
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchServiceListeners,
  fetchVolumes,
  redeployService,
  removeServiceDeployment,
  restartServiceDeployment,
  updateService,
} from "@/api";
import type {
  Deployment,
  ImageCredential,
  Service,
  ServiceDomain,
  ServiceListener,
  UpdateServiceInput,
  Volume,
} from "@/api";
import { DeploymentHistory } from "@/deployment-history";
import type { ResourceNodeData } from "@/project-flow";
import { deploymentPath } from "@/project-resource-path";
import { ResourceConsole } from "@/resource-console";
import { ResourceUsage } from "@/resource-usage";
import { serviceListenerKey } from "@/service-listeners";
import { ServiceSettings } from "@/service-settings";
import type { ServiceSettingsValues } from "@/service-settings";
import { ServiceVariables } from "@/service-variables";
import { WorkspaceView } from "@/workspace-view";

export type ServiceWorkspaceView =
  | "deployments"
  | "metrics"
  | "variables"
  | "console"
  | "settings";

interface ServiceDetailPanelProperties {
  data: ResourceNodeData;
  onChanged: () => void;
  projectID: string;
  serviceID: string;
  view: ServiceWorkspaceView;
}

const serviceUpdate = (
  service: Service,
  enabled: boolean,
  volumeMounts: Service["volumeMounts"] = service.volumeMounts
): UpdateServiceInput => ({
  args: service.args,
  command: service.command,
  cpuMillicores: service.cpuMillicores,
  enabled,
  environment: service.environment,
  expectedUpdatedAt: service.updatedAt,
  healthCheck: service.healthCheck,
  imageCredentialId: service.imageCredentialId,
  imageReference: service.imageReference,
  memoryMaxBytes: service.memoryMaxBytes,
  secretReferences: service.secretReferences,
  volumeMounts,
});

const ServicePanelError = ({
  error,
  hidden,
}: {
  error: string | null;
  hidden: boolean;
}) => {
  if (!(error && !hidden)) {
    return null;
  }
  return (
    <p className="border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
      {error}
    </p>
  );
};

export const ServiceDetailPanel = ({
  data,
  onChanged,
  projectID,
  serviceID,
  view,
}: ServiceDetailPanelProperties) => {
  const navigate = useNavigate();
  const [service, setService] = useState<Service | null>(null);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [domains, setDomains] = useState<ServiceDomain[]>([]);
  const [listeners, setListeners] = useState<ServiceListener[]>([]);
  const [volumes, setVolumes] = useState<Volume[]>([]);
  const [credentials, setCredentials] = useState<ImageCredential[]>([]);
  const [embeddedRegistryHost, setEmbeddedRegistryHost] = useState("");
  const [nextCursor, setNextCursor] = useState<string>();
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      const [
        loadedService,
        page,
        loadedDomains,
        loadedListeners,
        loadedVolumes,
        loadedCredentials,
        registrySettings,
      ] = await Promise.all([
        fetchService(projectID, serviceID, signal),
        fetchServiceDeployments(projectID, serviceID, undefined, signal),
        fetchServiceDomains(projectID, serviceID, signal),
        fetchServiceListeners(projectID, serviceID, signal),
        fetchVolumes(projectID, serviceID, signal),
        fetchImageCredentials(projectID, signal),
        fetchRegistrySettings(signal),
      ]);
      setService(loadedService);
      setDeployments(page.deployments);
      setNextCursor(page.nextCursor);
      setDomains(loadedDomains);
      setListeners(loadedListeners);
      setVolumes(loadedVolumes);
      setCredentials(loadedCredentials);
      setEmbeddedRegistryHost(registrySettings.hostname);
      setError(null);
    },
    [projectID, serviceID]
  );

  useEffect(() => {
    const controller = new AbortController();
    const loadService = async () => {
      try {
        await load(controller.signal);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load service"
          );
        }
      }
    };
    void loadService();
    return () => controller.abort();
  }, [load]);

  const apply = async (
    name: string,
    action: () => Promise<Service>
  ): Promise<boolean> => {
    if (busy) {
      return false;
    }
    setBusy(name);
    setError(null);
    try {
      setService(await action());
      const page = await fetchServiceDeployments(projectID, serviceID);
      setDeployments(page.deployments);
      setNextCursor(page.nextCursor);
      onChanged();
      return true;
    } catch (actionError) {
      setError(
        actionError instanceof Error
          ? actionError.message
          : `Unable to ${name} service`
      );
      return false;
    } finally {
      setBusy(undefined);
    }
  };

  const loadOlder = async () => {
    if (!(nextCursor && !busy)) {
      return;
    }
    setBusy("load history");
    try {
      const page = await fetchServiceDeployments(
        projectID,
        serviceID,
        nextCursor
      );
      setDeployments((current) => [...current, ...page.deployments]);
      setNextCursor(page.nextCursor);
    } catch (loadError) {
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to load deployment history"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const saveSettings = async (
    values: ServiceSettingsValues
  ): Promise<boolean> => {
    if (!service || busy) {
      return false;
    }
    setBusy("save settings");
    setError(null);
    try {
      const updated = await updateService(projectID, serviceID, {
        ...serviceUpdate(service, service.enabled, values.volumeMounts),
        healthCheck: values.healthCheck,
        imageCredentialId: values.imageCredentialId,
        imageReference: values.imageReference,
      });
      const domainUpdates = values.domains
        .filter(
          (domain) =>
            domains.find((current) => current.hostname === domain.hostname)
              ?.targetPort !== domain.targetPort
        )
        .map((domain) =>
          attachServiceDomain(
            projectID,
            serviceID,
            domain.hostname,
            domain.targetPort
          )
        );
      const listenerUpdates = values.listeners
        .filter(
          (listener) =>
            listeners.find(
              (current) =>
                serviceListenerKey(current) === serviceListenerKey(listener)
            )?.targetPort !== listener.targetPort
        )
        .map((listener) =>
          attachServiceListener(projectID, serviceID, {
            protocol: listener.protocol,
            publicPort: listener.publicPort,
            targetPort: listener.targetPort,
          })
        );
      const [, page] = await Promise.all([
        Promise.all([...domainUpdates, ...listenerUpdates]),
        fetchServiceDeployments(projectID, serviceID),
      ]);
      setService(updated);
      setDomains(values.domains);
      setListeners(values.listeners);
      setDeployments(page.deployments);
      setNextCursor(page.nextCursor);
      onChanged();
      return true;
    } catch (saveError) {
      try {
        await load();
      } catch {
        // The original mutation error is more useful than a follow-up refresh
        // failure and the next page load will reconcile the visible state.
      }
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to save service settings"
      );
      return false;
    } finally {
      setBusy(undefined);
    }
  };

  const deleteCurrentService = async (): Promise<boolean> => {
    if (!service || busy) {
      return false;
    }
    setBusy("delete service");
    setError(null);
    try {
      await deleteService(projectID, serviceID, service.updatedAt);
      onChanged();
      void navigate(`/projects/${encodeURIComponent(projectID)}`);
      return true;
    } catch (deleteError) {
      setError(
        deleteError instanceof Error
          ? deleteError.message
          : "Unable to delete service"
      );
      return false;
    } finally {
      setBusy(undefined);
    }
  };

  const saveVariables = (environment: Record<string, string>) => {
    if (!service) {
      return Promise.resolve(false);
    }
    return apply("save variables", () =>
      updateService(projectID, serviceID, {
        ...serviceUpdate(service, service.enabled),
        environment,
      })
    );
  };

  return (
    <div>
      <WorkspaceView
        active={view}
        views={{
          console: (
            <ResourceConsole
              projectID={projectID}
              resourceID={serviceID}
              resourceKind="service"
              resourceName={data.name}
            />
          ),
          deployments: (
            <DeploymentHistory
              activeDeploymentID={service?.activeDeploymentId}
              busy={Boolean(busy)}
              deployments={deployments}
              nextCursor={nextCursor}
              onDeployVersion={(deployment) => {
                if (service) {
                  void apply("deploy version", () =>
                    deployServiceVersion(
                      projectID,
                      serviceID,
                      deployment.id,
                      service.updatedAt
                    )
                  );
                }
              }}
              onLoadOlder={() => void loadOlder()}
              onRedeploy={() => {
                if (service) {
                  void apply("redeploy", () =>
                    redeployService(projectID, serviceID, service.updatedAt)
                  );
                }
              }}
              onRemove={(deployment) => {
                if (service) {
                  void apply("remove deployment", () =>
                    removeServiceDeployment(
                      projectID,
                      serviceID,
                      deployment.id,
                      service.updatedAt
                    )
                  );
                }
              }}
              onRestart={(deployment) => {
                if (service) {
                  void apply("restart", () =>
                    restartServiceDeployment(
                      projectID,
                      serviceID,
                      deployment.id,
                      service.updatedAt
                    )
                  );
                }
              }}
              onViewLogs={(deployment) =>
                void navigate(
                  deploymentPath(projectID, serviceID, deployment.id)
                )
              }
            />
          ),
          metrics: (
            <ResourceUsage
              cpuMillicores={service?.cpuMillicores}
              kind="service"
              memoryBytes={service?.memoryMaxBytes}
              resourceID={serviceID}
            />
          ),
          settings: service ? (
            <ServiceSettings
              actionError={error}
              busy={Boolean(busy)}
              credentials={credentials}
              domains={domains}
              embeddedRegistryHost={embeddedRegistryHost}
              internalHostname={data.internalHostname}
              key={service.updatedAt}
              listeners={listeners}
              onCredentialCreated={(credential) =>
                setCredentials((current) => [...current, credential])
              }
              onDelete={deleteCurrentService}
              onDomainsChange={setDomains}
              onListenersChange={setListeners}
              onSave={saveSettings}
              onVolumesChange={setVolumes}
              projectID={projectID}
              service={service}
              serviceID={serviceID}
              volumes={volumes}
            />
          ) : null,
          variables: service ? (
            <ServiceVariables
              busy={Boolean(busy)}
              key={service.updatedAt}
              onSave={saveVariables}
              projectID={projectID}
              service={service}
            />
          ) : null,
        }}
      />
      <ServicePanelError error={error} hidden={view === "settings"} />
    </div>
  );
};
