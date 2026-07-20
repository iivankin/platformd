import { Box, Database, HardDrive, X } from "lucide-react";
import type { ComponentType } from "react";
import { Link, Navigate } from "react-router";

import { PageTabs } from "@/page-tabs";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import { resourcePath } from "@/project-resource-path";
import { ResourceDraftBackups } from "@/resource-draft-backups";
import { ResourceDraftSettings } from "@/resource-draft-settings";
import { ResourceDraftVariables } from "@/resource-draft-variables";
import { ResourceDrawer } from "@/resource-drawer";
import { WorkspaceView } from "@/workspace-view";

type ManagedDraft = Exclude<PendingResourceCreation, { kind: "service" }>;
type ResourceDraftView = "backups" | "settings" | "variables";

const draftViews: { label: string; value: ResourceDraftView }[] = [
  { label: "Backups", value: "backups" },
  { label: "Variables", value: "variables" },
  { label: "Settings", value: "settings" },
];

const definitions: Record<
  ManagedDraft["kind"],
  {
    icon: ComponentType<{ className?: string }>;
    label: string;
    resourceKind: "object_store" | "postgres" | "redis";
  }
> = {
  postgres: { icon: Database, label: "PostgreSQL", resourceKind: "postgres" },
  redis: { icon: Box, label: "Redis", resourceKind: "redis" },
  storage: {
    icon: HardDrive,
    label: "Object storage",
    resourceKind: "object_store",
  },
};

export const ResourceDraftPage = ({
  draft,
  onChange,
  projectID,
  projectName,
  view,
}: {
  draft: ManagedDraft;
  onChange: (draft: ManagedDraft) => void;
  projectID: string;
  projectName: string;
  view: string;
}) => {
  const closePath = `/projects/${encodeURIComponent(projectID)}`;
  const definition = definitions[draft.kind];
  const validView = draftViews.some((candidate) => candidate.value === view);
  if (!validView) {
    return (
      <Navigate
        replace
        to={resourcePath(
          projectID,
          draft.id,
          definition.resourceKind,
          "variables"
        )}
      />
    );
  }

  const tabs = draftViews.map((candidate) => ({
    label: candidate.label,
    path: resourcePath(
      projectID,
      draft.id,
      definition.resourceKind,
      candidate.value
    ),
  }));
  const internalHostname = `${draft.input.name}.${projectName}.internal`;
  const Icon = definition.icon;

  return (
    <ResourceDrawer closePath={closePath} label={`${draft.input.name} draft`}>
      <section className="flex min-h-20 items-center gap-4 border-b border-border px-5 py-4">
        <Link
          aria-label={`Close ${definition.label} draft`}
          className="grid size-8 shrink-0 place-items-center border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          to={closePath}
        >
          <X className="size-3.5" />
        </Link>
        <span className="grid size-9 shrink-0 place-items-center bg-muted/50">
          <Icon className="size-4 text-muted-foreground" />
        </span>
        <div className="min-w-0">
          <p className="text-[8px] tracking-[0.14em] text-muted-foreground uppercase">
            {projectName} / {definition.label} draft
          </p>
          <h2 className="mt-1 truncate text-sm font-medium">
            {draft.input.name}
          </h2>
          <p className="mt-1 truncate text-[9px] text-muted-foreground">
            {internalHostname}
          </p>
        </div>
        <div className="ml-auto flex items-center gap-2 text-[9px] text-sky-600 dark:text-sky-300">
          <span className="size-1.5 bg-sky-500" />
          <span>Draft</span>
        </div>
      </section>
      <PageTabs label={`${draft.input.name} draft pages`} tabs={tabs} />
      <div className="min-h-0 flex-1 overflow-auto">
        <WorkspaceView
          active={view}
          views={{
            backups: (
              <ResourceDraftBackups
                onChange={(backupPolicy) =>
                  onChange({ ...draft, backupPolicy } as ManagedDraft)
                }
                policy={draft.backupPolicy}
              />
            ),
            settings: (
              <ResourceDraftSettings
                draft={draft}
                internalHostname={internalHostname}
                onChange={onChange}
              />
            ),
            variables: (
              <ResourceDraftVariables draft={draft} projectName={projectName} />
            ),
          }}
        />
      </div>
    </ResourceDrawer>
  );
};
