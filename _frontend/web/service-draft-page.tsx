import { Server, X } from "lucide-react";
import { Link, Navigate } from "react-router";

import { PageTabs } from "@/page-tabs";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import { resourcePath } from "@/project-resource-path";
import { ResourceDrawer } from "@/resource-drawer";
import { ServiceDraftSettings } from "@/service-draft-settings";
import { ServiceVariables } from "@/service-variables";
import { WorkspaceView } from "@/workspace-view";

type ServiceDraft = Extract<PendingResourceCreation, { kind: "service" }>;
type ServiceDraftView = "settings" | "variables";

const draftViews: { label: string; value: ServiceDraftView }[] = [
  { label: "Variables", value: "variables" },
  { label: "Settings", value: "settings" },
];

export const ServiceDraftPage = ({
  draft,
  embeddedRegistryHost,
  onChange,
  projectID,
  projectName,
  view,
}: {
  draft: ServiceDraft;
  embeddedRegistryHost: string;
  onChange: (draft: ServiceDraft) => void;
  projectID: string;
  projectName: string;
  view: string;
}) => {
  const closePath = `/projects/${encodeURIComponent(projectID)}`;
  const validView = draftViews.some((candidate) => candidate.value === view);
  if (!validView) {
    return (
      <Navigate
        replace
        to={resourcePath(projectID, draft.id, "service", "variables")}
      />
    );
  }

  const tabs = draftViews.map((candidate) => ({
    label: candidate.label,
    path: resourcePath(projectID, draft.id, "service", candidate.value),
  }));
  const internalHostname = `${draft.input.name}.${projectName}.internal`;

  return (
    <ResourceDrawer closePath={closePath} label={`${draft.input.name} draft`}>
      <section className="flex min-h-20 items-center gap-4 border-b border-border px-5 py-4">
        <Link
          aria-label="Close service draft"
          className="grid size-8 shrink-0 place-items-center border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          to={closePath}
        >
          <X className="size-3.5" />
        </Link>
        <span className="grid size-9 shrink-0 place-items-center bg-muted/50">
          <Server className="size-4 text-muted-foreground" />
        </span>
        <div className="min-w-0">
          <p className="text-[8px] tracking-[0.14em] text-muted-foreground uppercase">
            {projectName} / Service draft
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
            settings: (
              <ServiceDraftSettings
                draft={draft}
                embeddedRegistryHost={embeddedRegistryHost}
                internalHostname={internalHostname}
                onChange={onChange}
                projectID={projectID}
              />
            ),
            variables: (
              <ServiceVariables
                busy={false}
                onSave={(environment) => {
                  onChange({
                    ...draft,
                    input: { ...draft.input, environment },
                  });
                  return Promise.resolve(true);
                }}
                projectID={projectID}
                resolvedRaw={false}
                service={{ environment: draft.input.environment, id: draft.id }}
              />
            ),
          }}
        />
      </div>
    </ResourceDrawer>
  );
};
