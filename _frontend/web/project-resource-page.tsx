import { Box, Database, HardDrive, Server, X } from "lucide-react";
import type { ComponentType } from "react";
import { useEffect, useState } from "react";
import { Link, Navigate, useParams } from "react-router";

import { fetchProjectCanvas } from "@/api";
import { ObjectStoreDetailPanel } from "@/object-store-detail-panel";
import type { ObjectStoreWorkspaceView } from "@/object-store-detail-panel";
import { PageTabs } from "@/page-tabs";
import { PostgresDetailPanel } from "@/postgres-detail-panel";
import type { PostgresWorkspaceView } from "@/postgres-detail-panel";
import { useProjectChanges } from "@/project-changes";
import { projectFlowElements } from "@/project-flow";
import type { ResourceFlowNode, ResourceNodeData } from "@/project-flow";
import { resourceKind, resourcePath } from "@/project-resource-path";
import { RedisDetailPanel } from "@/redis-detail-panel";
import type { RedisWorkspaceView } from "@/redis-detail-panel";
import { ResourceDrawer } from "@/resource-drawer";
import { ServiceDetailPanel } from "@/service-detail-panel";
import type { ServiceWorkspaceView } from "@/service-detail-panel";
import type { PendingServiceSettings } from "@/service-settings-model";

interface ResourceWorkspaceDefinition {
  icon: ComponentType<{ className?: string }>;
  label: string;
  views: { label: string; value: string }[];
}

const workspaces: Record<
  ResourceNodeData["kind"],
  ResourceWorkspaceDefinition
> = {
  object_store: {
    icon: HardDrive,
    label: "Object storage",
    views: [
      { label: "Data", value: "objects" },
      { label: "Backups", value: "backups" },
      { label: "Variables", value: "variables" },
      { label: "Logs", value: "logs" },
      { label: "Settings", value: "settings" },
    ],
  },
  postgres: {
    icon: Database,
    label: "PostgreSQL",
    views: [
      { label: "Deployments", value: "deployments" },
      { label: "Database", value: "database" },
      { label: "Backups", value: "backups" },
      { label: "Variables", value: "variables" },
      { label: "Metrics", value: "metrics" },
      { label: "Console", value: "console" },
      { label: "Settings", value: "settings" },
    ],
  },
  redis: {
    icon: Box,
    label: "Redis",
    views: [
      { label: "Deployments", value: "deployments" },
      { label: "Database", value: "database" },
      { label: "Backups", value: "backups" },
      { label: "Variables", value: "variables" },
      { label: "Metrics", value: "metrics" },
      { label: "Console", value: "console" },
      { label: "Settings", value: "settings" },
    ],
  },
  service: {
    icon: Server,
    label: "Service",
    views: [
      { label: "Deployments", value: "deployments" },
      { label: "Variables", value: "variables" },
      { label: "Metrics", value: "metrics" },
      { label: "Console", value: "console" },
      { label: "Settings", value: "settings" },
    ],
  },
};

const statusColor: Record<ResourceNodeData["status"], string> = {
  degraded: "bg-amber-500",
  disabled: "bg-muted-foreground",
  failed: "bg-destructive",
  pending: "bg-sky-500",
  running: "bg-emerald-500",
};

const ResourceWorkspace = ({
  node,
  onChanged,
  onPendingSettingsChange,
  pendingSettings,
  projectID,
  view,
}: {
  node: ResourceFlowNode;
  onChanged: () => void;
  onPendingSettingsChange: (change?: PendingServiceSettings) => void;
  pendingSettings?: PendingServiceSettings;
  projectID: string;
  view: string;
}) => {
  switch (node.data.kind) {
    case "service": {
      return (
        <ServiceDetailPanel
          data={node.data}
          onChanged={onChanged}
          onPendingSettingsChange={onPendingSettingsChange}
          pendingSettings={pendingSettings}
          projectID={projectID}
          serviceID={node.id}
          view={view as ServiceWorkspaceView}
        />
      );
    }
    case "redis": {
      return (
        <RedisDetailPanel
          data={node.data}
          onChanged={onChanged}
          projectID={projectID}
          redisID={node.id}
          view={view as RedisWorkspaceView}
        />
      );
    }
    case "postgres": {
      return (
        <PostgresDetailPanel
          data={node.data}
          onChanged={onChanged}
          postgresID={node.id}
          projectID={projectID}
          view={view as PostgresWorkspaceView}
        />
      );
    }
    case "object_store": {
      return (
        <ObjectStoreDetailPanel
          data={node.data}
          projectID={projectID}
          storeID={node.id}
          view={view as ObjectStoreWorkspaceView}
        />
      );
    }
    default: {
      return null;
    }
  }
};

export const ProjectResourcePage = () => {
  const {
    projectID = "",
    resourceCollection = "",
    resourceID = "",
    view = "",
  } = useParams();
  const projectPath = `/projects/${encodeURIComponent(projectID)}`;
  const { serviceChanges, setServiceChange } = useProjectChanges(projectID);
  const kind = resourceKind(resourceCollection);
  const [node, setNode] = useState<ResourceFlowNode>();
  const [projectName, setProjectName] = useState("");
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [refreshVersion, setRefreshVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const canvas = await fetchProjectCanvas(projectID, controller.signal);
        const resourceNode = projectFlowElements(canvas).nodes.find(
          (candidate) =>
            candidate.id === resourceID &&
            (!kind || candidate.data.kind === kind)
        );
        setProjectName(canvas.project.name);
        setNode(resourceNode);
        setError(
          resourceNode ? undefined : "This resource is not part of the project."
        );
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load resource"
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
  }, [kind, projectID, refreshVersion, resourceID]);

  if (loading) {
    return (
      <ResourceDrawer closePath={projectPath} label="Loading resource">
        <div className="grid h-full place-items-center text-[10px] text-muted-foreground">
          Loading resource…
        </div>
      </ResourceDrawer>
    );
  }
  if (!(kind && node)) {
    return (
      <ResourceDrawer closePath={projectPath} label="Resource unavailable">
        <div className="grid h-full place-items-center px-8 text-center">
          <div>
            <p className="text-xs font-medium">Resource unavailable</p>
            <p className="mt-2 text-[10px] text-muted-foreground">
              {error ?? "The resource route is invalid."}
            </p>
            <Link
              className="mt-4 inline-flex text-[10px] underline underline-offset-4"
              to={projectPath}
            >
              Return to project canvas
            </Link>
          </div>
        </div>
      </ResourceDrawer>
    );
  }

  const definition = workspaces[kind];
  const validView = definition.views.some(
    (candidate) => candidate.value === view
  );
  if (!validView) {
    return (
      <Navigate
        replace
        to={resourcePath(
          projectID,
          resourceID,
          kind,
          definition.views[0]?.value ?? "settings"
        )}
      />
    );
  }

  const Icon = definition.icon;
  const tabs = definition.views.map((candidate) => ({
    label: candidate.label,
    path: resourcePath(projectID, resourceID, kind, candidate.value),
  }));

  return (
    <ResourceDrawer
      closePath={projectPath}
      label={`${node.data.name} resource`}
    >
      <section className="flex min-h-20 items-center gap-4 border-b border-border px-5 py-4">
        <Link
          aria-label="Close resource drawer"
          className="grid size-8 shrink-0 place-items-center border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          to={projectPath}
        >
          <X className="size-3.5" />
        </Link>
        <span className="grid size-9 shrink-0 place-items-center bg-muted/50">
          <Icon className="size-4 text-muted-foreground" />
        </span>
        <div className="min-w-0">
          <p className="text-[8px] tracking-[0.14em] text-muted-foreground uppercase">
            {projectName} / {definition.label}
          </p>
          <h2 className="mt-1 truncate text-sm font-medium">
            {node.data.name}
          </h2>
          <p className="mt-1 truncate text-[9px] text-muted-foreground">
            {node.data.internalHostname}
          </p>
        </div>
        <div className="ml-auto flex items-center gap-2 text-[9px] text-muted-foreground">
          <span className={`size-1.5 ${statusColor[node.data.status]}`} />
          <span className="capitalize">{node.data.status}</span>
        </div>
      </section>
      <PageTabs label={`${node.data.name} resource pages`} tabs={tabs} />
      <div className="min-h-0 flex-1 overflow-auto">
        <ResourceWorkspace
          node={node}
          onChanged={() => setRefreshVersion((value) => value + 1)}
          onPendingSettingsChange={(change) =>
            setServiceChange(resourceID, change)
          }
          pendingSettings={serviceChanges[resourceID]}
          projectID={projectID}
          view={view}
        />
      </div>
    </ResourceDrawer>
  );
};
