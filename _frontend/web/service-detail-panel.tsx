import { Power, RefreshCw, Server, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import {
  fetchService,
  fetchServiceDeployments,
  redeployService,
  rollbackService,
  updateService,
} from "@/api";
import type { Deployment, Service, UpdateServiceInput } from "@/api";
import { Button } from "@/components/ui/button";
import { DeploymentHistory } from "@/deployment-history";
import type { ResourceNodeData } from "@/project-flow";

interface ServiceDetailPanelProperties {
  data: ResourceNodeData;
  onChanged: () => void;
  onClose: () => void;
  projectID: string;
  serviceID: string;
}

const serviceUpdate = (
  service: Service,
  enabled: boolean
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
  secretReferences: service.secretReferences,
  startupTimeoutSeconds: service.startupTimeoutSeconds,
  targetPort: service.targetPort,
  volumeMounts: service.volumeMounts,
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

export const ServiceDetailPanel = ({
  data,
  onChanged,
  onClose,
  projectID,
  serviceID,
}: ServiceDetailPanelProperties) => {
  const [service, setService] = useState<Service | null>(null);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [nextCursor, setNextCursor] = useState<string>();
  const [rollbackCandidate, setRollbackCandidate] = useState<string>();
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(
    async (signal?: AbortSignal) => {
      const [loadedService, page] = await Promise.all([
        fetchService(projectID, serviceID, signal),
        fetchServiceDeployments(projectID, serviceID, undefined, signal),
      ]);
      setService(loadedService);
      setDeployments(page.deployments);
      setNextCursor(page.nextCursor);
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

  const apply = async (name: string, action: () => Promise<Service>) => {
    if (busy) {
      return;
    }
    setBusy(name);
    setError(null);
    try {
      setService(await action());
      const page = await fetchServiceDeployments(projectID, serviceID);
      setDeployments(page.deployments);
      setNextCursor(page.nextCursor);
      setRollbackCandidate(undefined);
      onChanged();
    } catch (actionError) {
      setError(
        actionError instanceof Error
          ? actionError.message
          : `Unable to ${name} service`
      );
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

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-lg overflow-y-auto border-l border-border bg-background shadow-[-8px_0_24px_oklch(0_0_0/5%)]">
      <div className="flex h-12 items-center border-b border-border px-4">
        <Server className="size-4 text-muted-foreground" />
        <div className="ml-2 min-w-0">
          <h2 className="truncate text-xs font-medium">{data.name}</h2>
          <p className="text-[9px] text-muted-foreground">Service</p>
        </div>
        <Button
          aria-label="Close service details"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>

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
                  redeployService(projectID, serviceID, service.updatedAt)
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
          <p aria-live="polite" className="mt-3 text-[10px] text-destructive">
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
          <Detail label="Target port" value={service?.targetPort?.toString()} />
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

      <DeploymentHistory
        busy={Boolean(busy)}
        deployments={deployments}
        nextCursor={nextCursor}
        onCancelRollback={() => setRollbackCandidate(undefined)}
        onLoadOlder={() => void loadOlder()}
        onRollback={(deployment) => {
          if (service) {
            void apply("rollback", () =>
              rollbackService(
                projectID,
                serviceID,
                deployment.id,
                service.updatedAt
              )
            );
          }
        }}
        onSelectRollback={setRollbackCandidate}
        rollbackCandidate={rollbackCandidate}
      />
    </aside>
  );
};
