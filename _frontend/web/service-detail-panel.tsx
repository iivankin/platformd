import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";

import {
  deleteService,
  deployServiceVersion,
  fetchRegistrySettings,
  fetchService,
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchServiceListeners,
  fetchServicePreviews,
  fetchVolumes,
  redeployService,
  removeServiceDeployment,
  restartServiceDeployment,
  updateService,
} from "@/api";
import type {
  Deployment,
  PreviewDeployment,
  Service,
  ServiceDomain,
  ServiceListener,
  UpdateServiceInput,
  Volume,
} from "@/api";
import { DeploymentHistory } from "@/deployment-history";
import { PreviewDeploymentHistory } from "@/preview-deployment-history";
import type { ResourceNodeData } from "@/project-flow";
import { deploymentPath } from "@/project-resource-path";
import { ResourceConsole } from "@/resource-console";
import { ResourceUsage } from "@/resource-usage";
import { ServiceSettings } from "@/service-settings";
import type { PendingServiceSettings } from "@/service-settings-model";
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
  onPendingSettingsChange: (change?: PendingServiceSettings) => void;
  pendingSettings?: PendingServiceSettings;
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
  memoryMaxBytes: service.memoryMaxBytes,
  secretReferences: service.secretReferences,
  source: service.source,
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
  onPendingSettingsChange,
  pendingSettings,
  projectID,
  serviceID,
  view,
}: ServiceDetailPanelProperties) => {
  const navigate = useNavigate();
  const [service, setService] = useState<Service | null>(null);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [previews, setPreviews] = useState<PreviewDeployment[]>([]);
  const [domains, setDomains] = useState<ServiceDomain[]>([]);
  const [listeners, setListeners] = useState<ServiceListener[]>([]);
  const [volumes, setVolumes] = useState<Volume[]>([]);
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
        registrySettings,
        loadedPreviews,
      ] = await Promise.all([
        fetchService(projectID, serviceID, signal),
        fetchServiceDeployments(projectID, serviceID, undefined, signal),
        fetchServiceDomains(projectID, serviceID, signal),
        fetchServiceListeners(projectID, serviceID, signal),
        fetchVolumes(projectID, serviceID, signal),
        fetchRegistrySettings(signal),
        fetchServicePreviews(projectID, serviceID, signal),
      ]);
      setService(loadedService);
      setDeployments(page.deployments);
      setNextCursor(page.nextCursor);
      setDomains(loadedDomains);
      setListeners(loadedListeners);
      setVolumes(loadedVolumes);
      setEmbeddedRegistryHost(registrySettings.hostname);
      setPreviews(loadedPreviews);
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

  const deleteCurrentService = async (): Promise<boolean> => {
    if (!service || busy) {
      return false;
    }
    setBusy("delete service");
    setError(null);
    try {
      await deleteService(projectID, serviceID, service.updatedAt);
      onPendingSettingsChange();
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
            <div className="grid gap-3">
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
              <PreviewDeploymentHistory
                onViewLogs={(preview) =>
                  void navigate(
                    deploymentPath(projectID, serviceID, preview.id)
                  )
                }
                previews={previews}
              />
            </div>
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
              domains={domains}
              embeddedRegistryHost={embeddedRegistryHost}
              internalHostname={data.internalHostname}
              key={service.updatedAt}
              listeners={listeners}
              onDelete={deleteCurrentService}
              onDraftChange={onPendingSettingsChange}
              onVolumesChange={setVolumes}
              pendingChange={pendingSettings}
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
