import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";

import {
  deleteService,
  deployServiceVersion,
  fetchService,
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchServiceListeners,
  fetchServicePreviews,
  fetchVolumes,
  redeployService,
  removeServiceDeployment,
  restartServiceDeployment,
} from "@/api";
import type {
  Deployment,
  PreviewDeployment,
  Service,
  ServiceDomain,
  ServiceListener,
  Volume,
} from "@/api";
import { ContainerFileBrowser } from "@/container-file-browser";
import { ContainerTerminalOverlay } from "@/container-terminal-overlay";
import { DeploymentHistory } from "@/deployment-history";
import { Modal } from "@/errors/dialog-frame";
import { PreviewDeploymentHistory } from "@/preview-deployment-history";
import type { ResourceNodeData } from "@/project-flow";
import { serviceTelemetryLogsPath } from "@/project-resource-path";
import { ServiceTelemetryWorkspace } from "@/service-errors";
import { ServiceSettings } from "@/service-settings";
import {
  createPendingServiceSettings,
  createServiceSettingsDraft,
} from "@/service-settings-model";
import type { PendingServiceSettings } from "@/service-settings-model";
import { ServiceVariables } from "@/service-variables";
import { WorkspaceView } from "@/workspace-view";

export type ServiceWorkspaceView =
  | "deployments"
  | "telemetry"
  | "variables"
  | "settings";

type RuntimeTool = "console" | "files";

interface ServiceDetailPanelProperties {
  data: ResourceNodeData;
  onChanged: () => void;
  onPendingSettingsChange: (change?: PendingServiceSettings) => void;
  pendingSettings?: PendingServiceSettings;
  projectID: string;
  serviceID: string;
  view: ServiceWorkspaceView;
}

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
  const [nextCursor, setNextCursor] = useState<string>();
  const [awaitingDeploymentAfter, setAwaitingDeploymentAfter] =
    useState<number>();
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string | null>(null);
  const [runtimeTool, setRuntimeTool] = useState<RuntimeTool>();

  const load = useCallback(
    async (signal?: AbortSignal) => {
      const [
        loadedService,
        page,
        loadedDomains,
        loadedListeners,
        loadedVolumes,
        loadedPreviews,
      ] = await Promise.all([
        fetchService(projectID, serviceID, signal),
        fetchServiceDeployments(projectID, serviceID, undefined, signal),
        fetchServiceDomains(projectID, serviceID, signal),
        fetchServiceListeners(projectID, serviceID, signal),
        fetchVolumes(projectID, serviceID, signal),
        fetchServicePreviews(projectID, serviceID, signal),
      ]);
      setService(loadedService);
      setDeployments(page.deployments);
      setNextCursor(page.nextCursor);
      setDomains(loadedDomains);
      setListeners(loadedListeners);
      setVolumes(loadedVolumes);
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

  const deploymentInProgress =
    deployments[0]?.status === "running" ||
    deployments[0]?.status === "waiting";

  useEffect(() => {
    if (
      view !== "deployments" ||
      !(deploymentInProgress || awaitingDeploymentAfter)
    ) {
      return;
    }
    const controller = new AbortController();
    const refresh = async () => {
      try {
        const [loadedService, page] = await Promise.all([
          fetchService(projectID, serviceID, controller.signal),
          fetchServiceDeployments(
            projectID,
            serviceID,
            undefined,
            controller.signal
          ),
        ]);
        setService(loadedService);
        setDeployments(page.deployments);
        setNextCursor(page.nextCursor);
        const [latest] = page.deployments;
        if (
          awaitingDeploymentAfter &&
          latest &&
          latest.createdAt >= awaitingDeploymentAfter
        ) {
          setAwaitingDeploymentAfter(undefined);
        }
      } catch (refreshError) {
        if (
          !(
            refreshError instanceof DOMException &&
            refreshError.name === "AbortError"
          )
        ) {
          setError(
            refreshError instanceof Error
              ? refreshError.message
              : "Unable to refresh deployment"
          );
        }
      }
    };
    const timer = globalThis.setInterval(() => void refresh(), 1000);
    return () => {
      globalThis.clearInterval(timer);
      controller.abort();
    };
  }, [
    awaitingDeploymentAfter,
    deploymentInProgress,
    projectID,
    serviceID,
    view,
  ]);

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
      const updated = await action();
      setService(updated);
      setAwaitingDeploymentAfter(updated.updatedAt);
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

  const saveVariables = ({
    environment,
  }: {
    environment?: Record<string, string>;
  }) => {
    if (!service) {
      return Promise.resolve(false);
    }
    onPendingSettingsChange(
      createPendingServiceSettings({
        current: pendingSettings,
        domains,
        draft:
          pendingSettings?.draft ??
          createServiceSettingsDraft(service, domains, listeners, volumes),
        environment,
        listeners,
        service,
        volumes,
      })
    );
    setError(null);
    return Promise.resolve(true);
  };

  return (
    <div>
      <WorkspaceView
        active={view}
        views={{
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
                onOpenConsole={() => setRuntimeTool("console")}
                onOpenFiles={() => setRuntimeTool("files")}
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
                    serviceTelemetryLogsPath(
                      projectID,
                      serviceID,
                      deployment.id
                    )
                  )
                }
              />
              <PreviewDeploymentHistory
                onViewLogs={(preview) =>
                  void navigate(
                    serviceTelemetryLogsPath(projectID, serviceID, preview.id)
                  )
                }
                previews={previews}
              />
            </div>
          ),
          settings: service ? (
            <ServiceSettings
              actionError={error}
              busy={Boolean(busy)}
              domains={domains}
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
          telemetry: (
            <ServiceTelemetryWorkspace
              cpuMillicores={service?.cpuMillicores}
              memoryBytes={service?.memoryMaxBytes}
              projectID={projectID}
              serviceID={serviceID}
            />
          ),
          variables: service ? (
            <ServiceVariables
              busy={Boolean(busy)}
              key={`${service.updatedAt}:${pendingSettings ? "pending" : "saved"}`}
              onSave={saveVariables}
              projectID={projectID}
              resolvedRaw={!pendingSettings}
              service={{
                environment:
                  pendingSettings?.environment ?? service.environment,
                id: service.id,
                source:
                  pendingSettings?.draft.configuration.source ?? service.source,
              }}
            />
          ) : null,
        }}
      />
      <ServicePanelError error={error} hidden={view === "settings"} />
      <Modal
        className="max-w-[min(76rem,calc(100vw-2rem))]"
        description={`Interactive shell for ${data.name}'s current deployment.`}
        onOpenChange={(open) => setRuntimeTool(open ? "console" : undefined)}
        open={runtimeTool === "console"}
        title="Container console"
      >
        <ContainerTerminalOverlay
          className="border-0 ring-0"
          embedded
          projectID={projectID}
          resourceID={serviceID}
          resourceKind="service"
          resourceName={data.name}
        />
      </Modal>
      <Modal
        className="max-w-[min(92rem,calc(100vw-2rem))]"
        description="Browse and transfer files in the currently running container."
        onOpenChange={(open) => setRuntimeTool(open ? "files" : undefined)}
        open={runtimeTool === "files"}
        title={`${data.name} container files`}
      >
        <ContainerFileBrowser
          className="border-0 ring-0"
          projectID={projectID}
          resourceID={serviceID}
          resourceKind="service"
        />
      </Modal>
    </div>
  );
};
