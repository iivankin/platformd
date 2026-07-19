import { Database, Server, X, Zap } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, Navigate, useParams } from "react-router";

import { fetchRuntimeDeployment, fetchServiceDeployment } from "@/api";
import type {
  Deployment,
  ManagedDeploymentKind,
  ProjectCanvas,
  RuntimeDeployment,
} from "@/api";
import { BuildLogs } from "@/build-logs";
import { DeploymentDetails } from "@/deployment-details";
import { DeploymentLogs } from "@/deployment-logs";
import { ManagedDeploymentDetails } from "@/managed-deployment-details";
import { PageTabs } from "@/page-tabs";
import {
  resourceDeploymentPath,
  resourceKind,
  resourcePath,
} from "@/project-resource-path";
import type { DeploymentWorkspaceView } from "@/project-resource-path";
import { ResourceDrawer } from "@/resource-drawer";

type AnyDeployment = Deployment | RuntimeDeployment;
type DeploymentResourceKind = ManagedDeploymentKind | "service";

const statusClass: Record<AnyDeployment["status"], string> = {
  failed: "border-destructive/50 text-destructive",
  interrupted: "border-amber-500/50 text-amber-700 dark:text-amber-300",
  removed: "border-muted-foreground/50 text-muted-foreground",
  running: "border-sky-500/50 text-sky-700 dark:text-sky-300",
  skipped: "border-muted-foreground/50 text-muted-foreground",
  succeeded: "border-emerald-500/50 text-emerald-700 dark:text-emerald-300",
  waiting: "border-amber-500/50 text-amber-700 dark:text-amber-300",
};

const shortID = (value: string) =>
  value.length > 18 ? `${value.slice(0, 14)}…` : value;

const iconByKind = {
  postgres: Database,
  redis: Zap,
  service: Server,
};

const isDeploymentKind = (
  kind: ReturnType<typeof resourceKind>
): kind is DeploymentResourceKind =>
  kind === "service" || kind === "postgres" || kind === "redis";

const useDeployment = (
  projectID: string,
  kind: DeploymentResourceKind | undefined,
  resourceID: string,
  deploymentID: string
) => {
  const [deployment, setDeployment] = useState<AnyDeployment>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      if (!kind) {
        setError("This resource does not have deployment history.");
        setLoading(false);
        return;
      }
      try {
        const loaded =
          kind === "service"
            ? await fetchServiceDeployment(
                projectID,
                resourceID,
                deploymentID,
                controller.signal
              )
            : await fetchRuntimeDeployment(
                projectID,
                kind,
                resourceID,
                deploymentID,
                controller.signal
              );
        setDeployment(loaded);
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load deployment"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [deploymentID, kind, projectID, resourceID]);
  return { deployment, error, loading };
};

const deploymentContent = ({
  deployment,
  kind,
  projectID,
  resourceID,
  view,
}: {
  deployment: AnyDeployment;
  kind: DeploymentResourceKind;
  projectID: string;
  resourceID: string;
  view: DeploymentWorkspaceView;
}) => {
  if (view === "build-logs" && kind === "service") {
    return (
      <BuildLogs
        deploymentID={deployment.id}
        projectID={projectID}
        running={
          deployment.status === "running" || deployment.status === "waiting"
        }
        serviceID={resourceID}
      />
    );
  }
  if (view === "deploy-logs") {
    return (
      <DeploymentLogs
        deploymentID={deployment.id}
        kind={kind}
        projectID={projectID}
        resourceID={resourceID}
      />
    );
  }
  return kind === "service" ? (
    <DeploymentDetails deployment={deployment as Deployment} />
  ) : (
    <ManagedDeploymentDetails deployment={deployment as RuntimeDeployment} />
  );
};

export const ProjectDeploymentPage = ({
  canvas,
}: {
  canvas: ProjectCanvas | null;
}) => {
  const {
    deploymentID = "",
    deploymentView = "",
    projectID = "",
    resourceCollection = "",
    resourceID = "",
  } = useParams();
  const kind = resourceKind(resourceCollection);
  const validKind = isDeploymentKind(kind) ? kind : undefined;
  const { deployment, error, loading } = useDeployment(
    projectID,
    validKind,
    resourceID,
    deploymentID
  );
  const closePath = validKind
    ? resourcePath(projectID, resourceID, validKind, "deployments")
    : `/projects/${encodeURIComponent(projectID)}`;

  const resource = canvas?.resources.find(
    (candidate) => candidate.kind === validKind && candidate.id === resourceID
  );
  if (loading || !canvas) {
    return (
      <ResourceDrawer closePath={closePath} label="Loading deployment">
        <div className="grid h-full place-items-center text-[10px] text-muted-foreground">
          Loading deployment…
        </div>
      </ResourceDrawer>
    );
  }
  if (!(deployment && resource && validKind)) {
    return (
      <ResourceDrawer closePath={closePath} label="Deployment unavailable">
        <div className="grid h-full place-items-center px-8 text-center">
          <div>
            <p className="text-xs font-medium">Deployment unavailable</p>
            <p className="mt-2 text-[10px] text-muted-foreground">
              {error ?? "This deployment is not part of the selected resource."}
            </p>
            <Link
              className="mt-4 inline-flex text-[10px] underline underline-offset-4"
              to={closePath}
            >
              Return to deployment history
            </Link>
          </div>
        </div>
      </ResourceDrawer>
    );
  }

  const validView =
    deploymentView === "details" ||
    deploymentView === "deploy-logs" ||
    (validKind === "service" && deploymentView === "build-logs");
  if (!validView) {
    return (
      <Navigate
        replace
        to={resourceDeploymentPath(
          projectID,
          resourceID,
          validKind,
          deploymentID
        )}
      />
    );
  }
  const view = deploymentView as DeploymentWorkspaceView;
  const tabs = [
    {
      label: "Details",
      path: resourceDeploymentPath(
        projectID,
        resourceID,
        validKind,
        deploymentID,
        "details"
      ),
    },
    ...(validKind === "service"
      ? [
          {
            label: "Build logs",
            path: resourceDeploymentPath(
              projectID,
              resourceID,
              validKind,
              deploymentID,
              "build-logs"
            ),
          },
        ]
      : []),
    {
      label: "Deploy logs",
      path: resourceDeploymentPath(
        projectID,
        resourceID,
        validKind,
        deploymentID,
        "deploy-logs"
      ),
    },
  ];
  const ResourceIcon = iconByKind[validKind];
  const content = deploymentContent({
    deployment,
    kind: validKind,
    projectID,
    resourceID,
    view,
  });

  return (
    <ResourceDrawer closePath={closePath} label={`${resource.name} deployment`}>
      <section className="flex min-h-24 items-start gap-4 border-b border-border px-5 py-4">
        <span className="grid size-9 shrink-0 place-items-center bg-muted/50">
          <ResourceIcon className="size-4 text-muted-foreground" />
        </span>
        <div className="min-w-0">
          <p className="text-[8px] tracking-[0.14em] text-muted-foreground uppercase">
            {canvas.project.name} / Deployment
          </p>
          <div className="mt-1 flex flex-wrap items-center gap-2">
            <h2 className="truncate text-sm font-medium">{resource.name}</h2>
            <span className="text-muted-foreground">/</span>
            <code className="text-xs" title={deployment.id}>
              {shortID(deployment.id)}
            </code>
            <span
              className={`border px-2 py-0.5 text-[8px] tracking-[0.1em] uppercase ${statusClass[deployment.status]}`}
            >
              {deployment.status}
            </span>
          </div>
          <p className="mt-2 truncate text-[9px] text-muted-foreground">
            {resource.internalHostname} ·{" "}
            {new Date(deployment.createdAt).toLocaleString()}
          </p>
        </div>
        <Link
          aria-label="Close deployment workspace"
          className="ml-auto grid size-8 shrink-0 place-items-center border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          to={closePath}
        >
          <X className="size-3.5" />
        </Link>
      </section>
      <PageTabs label={`${resource.name} deployment pages`} tabs={tabs} />
      <div className="min-h-0 flex-1 overflow-auto">{content}</div>
    </ResourceDrawer>
  );
};
