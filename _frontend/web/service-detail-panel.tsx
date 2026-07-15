import { Power, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";

import {
  deployServiceVersion,
  fetchService,
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchVolumes,
  redeployService,
  removeServiceDeployment,
  restartServiceDeployment,
  updateService,
} from "@/api";
import type {
  Deployment,
  Service,
  ServiceDomain,
  UpdateServiceInput,
  Volume,
} from "@/api";
import { Button } from "@/components/ui/button";
import { DeploymentHistory } from "@/deployment-history";
import type { ResourceNodeData } from "@/project-flow";
import { deploymentPath } from "@/project-resource-path";
import { ResourceConsole } from "@/resource-console";
import { ResourceUsage } from "@/resource-usage";
import { ServiceConfiguration } from "@/service-configuration";
import type { ServiceConfigurationValues } from "@/service-configuration";
import { ServiceDomains } from "@/service-domains";
import { ServiceVariables } from "@/service-variables";
import { ServiceVolumes } from "@/service-volumes";
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
  healthPath: service.healthPath,
  imageCredentialId: service.imageCredentialId,
  imageReference: service.imageReference,
  memoryMaxBytes: service.memoryMaxBytes,
  resourceReferences: service.resourceReferences,
  secretReferences: service.secretReferences,
  startupTimeoutSeconds: service.startupTimeoutSeconds,
  targetPort: service.targetPort,
  volumeMounts,
});

const statusColor = (status: ResourceNodeData["status"]) => {
  const colors: Record<ResourceNodeData["status"], string> = {
    degraded: "bg-amber-500",
    disabled: "bg-muted-foreground",
    failed: "bg-destructive",
    pending: "bg-sky-500",
    running: "bg-emerald-500",
  };
  return colors[status];
};

const Detail = ({ label, value }: { label: string; value?: string }) => (
  <div className="grid grid-cols-[7rem_minmax(0,1fr)] gap-3 border-b border-border py-2.5 last:border-b-0">
    <dt className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
      {label}
    </dt>
    <dd className="min-w-0 text-[10px] leading-4 break-all">{value ?? "—"}</dd>
  </div>
);

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
  const [volumes, setVolumes] = useState<Volume[]>([]);
  const [nextCursor, setNextCursor] = useState<string>();
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      const [loadedService, page, loadedDomains, loadedVolumes] =
        await Promise.all([
          fetchService(projectID, serviceID, signal),
          fetchServiceDeployments(projectID, serviceID, undefined, signal),
          fetchServiceDomains(projectID, serviceID, signal),
          fetchVolumes(projectID, serviceID, signal),
        ]);
      setService(loadedService);
      setDeployments(page.deployments);
      setNextCursor(page.nextCursor);
      setDomains(loadedDomains);
      setVolumes(loadedVolumes);
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

  const saveConfiguration = (values: ServiceConfigurationValues) => {
    if (!service) {
      return Promise.resolve(false);
    }
    return apply("save configuration", () =>
      updateService(projectID, serviceID, {
        ...serviceUpdate(service, service.enabled),
        ...values,
      })
    );
  };

  const saveVariables = (
    environment: Record<string, string>,
    resourceReferences: Service["resourceReferences"]
  ) => {
    if (!service) {
      return Promise.resolve(false);
    }
    return apply("save variables", () =>
      updateService(projectID, serviceID, {
        ...serviceUpdate(service, service.enabled),
        environment,
        resourceReferences,
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
          settings: (
            <>
              <section className="border-b border-border px-4 py-4">
                <div className="flex items-center gap-2">
                  <span className={`size-1.5 ${statusColor(data.status)}`} />
                  <span className="text-[10px] font-medium capitalize">
                    {data.status}
                  </span>
                </div>
                {data.statusMessage ? (
                  <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
                    {data.statusMessage}
                  </p>
                ) : null}
              </section>

              <section className="border-b border-border px-4 py-4">
                <div className="flex flex-wrap gap-2">
                  <Button
                    disabled={!service || Boolean(busy)}
                    onClick={() => {
                      if (service) {
                        void apply(service.enabled ? "disable" : "enable", () =>
                          updateService(
                            projectID,
                            serviceID,
                            serviceUpdate(service, !service.enabled)
                          )
                        );
                      }
                    }}
                    size="sm"
                    variant={service?.enabled ? "destructive" : "default"}
                  >
                    <Power />
                    {service?.enabled ? "Disable" : "Enable"}
                  </Button>
                  <Button
                    disabled={!service?.enabled || Boolean(busy)}
                    onClick={() => {
                      if (service) {
                        void apply("redeploy", () =>
                          redeployService(
                            projectID,
                            serviceID,
                            service.updatedAt
                          )
                        );
                      }
                    }}
                    size="sm"
                    variant="outline"
                  >
                    <RefreshCw />
                    Redeploy
                  </Button>
                </div>
                {error ? (
                  <p
                    aria-live="polite"
                    className="mt-3 text-[10px] text-destructive"
                  >
                    {error}
                  </p>
                ) : null}
              </section>

              <section className="border-b border-border px-4 py-4">
                <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
                  Runtime configuration
                </h3>
                <dl className="mt-2">
                  <Detail label="Internal DNS" value={data.internalHostname} />
                  <Detail label="Image" value={service?.imageReference} />
                  <Detail label="Digest" value={service?.activeImageDigest} />
                  <Detail
                    label="Target port"
                    value={service?.targetPort?.toString()}
                  />
                  <Detail
                    label="Updated"
                    value={
                      service
                        ? new Date(service.updatedAt).toLocaleString()
                        : "Loading…"
                    }
                  />
                </dl>
              </section>
              {service ? (
                <>
                  <ServiceConfiguration
                    busy={Boolean(busy)}
                    key={service.updatedAt}
                    onSave={saveConfiguration}
                    service={service}
                  />
                  <ServiceVolumes
                    onMountsChange={(mounts) =>
                      apply("update volume mounts", () =>
                        updateService(
                          projectID,
                          serviceID,
                          serviceUpdate(service, service.enabled, mounts)
                        )
                      )
                    }
                    onVolumesChange={setVolumes}
                    projectID={projectID}
                    service={service}
                    serviceID={serviceID}
                    volumes={volumes}
                  />
                  <ServiceDomains
                    domains={domains}
                    onChanged={setDomains}
                    projectID={projectID}
                    serviceID={serviceID}
                    targetPort={service.targetPort}
                  />
                </>
              ) : null}
            </>
          ),
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
