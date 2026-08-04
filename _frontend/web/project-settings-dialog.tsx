import { Dialog } from "@base-ui/react/dialog";
import {
  Activity,
  Check,
  Copy,
  FolderKanban,
  ScrollText,
  Settings,
  Webhook,
  X,
} from "lucide-react";
import { useState } from "react";

import type { Project } from "@/api";
import { AuditEventsView } from "@/audit-events-view";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ProjectDeleteDialog } from "@/project-delete-dialog";
import { ProjectWebhooksSettings } from "@/project-webhooks-settings";
import { ProjectUsage } from "@/resource-usage";

type ProjectSettingsSection = "audit" | "general" | "usage" | "webhooks";

const projectResourceCount = (project: Project) =>
  project.serviceCount +
  project.postgresCount +
  project.redisCount +
  project.objectStoreCount +
  project.networkGatewayCount;

const projectSettingsSections = [
  { icon: FolderKanban, label: "General", value: "general" },
  { icon: Activity, label: "Usage", value: "usage" },
  { icon: ScrollText, label: "Audit", value: "audit" },
  { icon: Webhook, label: "Webhooks", value: "webhooks" },
] as const;

const formatDate = (timestamp: number) =>
  new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(timestamp);

const projectDetailRowClassName =
  "grid min-h-14 grid-cols-[9rem_minmax(0,1fr)_auto] items-center gap-x-3 border-b border-border px-6 py-3 text-[10px]";

const ProjectGeneralSettings = ({
  onDeleted,
  project,
}: {
  onDeleted: (projectID: string) => void;
  project: Project;
}) => {
  const [copyStatus, setCopyStatus] = useState<"copied" | "error" | "idle">(
    "idle"
  );
  const resources = projectResourceCount(project);

  const copyProjectID = async () => {
    try {
      await navigator.clipboard.writeText(project.id);
      setCopyStatus("copied");
    } catch {
      setCopyStatus("error");
    }
  };

  return (
    <div>
      <header className="border-b border-border px-6 py-5">
        <h3 className="text-sm font-medium">General</h3>
        <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
          Project identity and lifecycle details.
        </p>
      </header>

      <dl>
        <div className={projectDetailRowClassName}>
          <dt className="text-muted-foreground">Name</dt>
          <dd className="truncate font-medium">{project.name}</dd>
        </div>
        <div className={projectDetailRowClassName}>
          <dt className="text-muted-foreground">Project ID</dt>
          <dd className="truncate font-mono" title={project.id}>
            {project.id}
          </dd>
          <Button
            aria-label="Copy project ID"
            onClick={() => void copyProjectID()}
            size="sm"
            variant="outline"
          >
            {copyStatus === "copied" ? <Check /> : <Copy />}
            {copyStatus === "copied" ? "Copied" : "Copy"}
          </Button>
        </div>
        <div className={projectDetailRowClassName}>
          <dt className="text-muted-foreground">Private namespace</dt>
          <dd className="truncate font-mono">{project.name}.internal</dd>
        </div>
        <div className={projectDetailRowClassName}>
          <dt className="text-muted-foreground">Resources</dt>
          <dd>{resources}</dd>
        </div>
        <div className={projectDetailRowClassName}>
          <dt className="text-muted-foreground">Created</dt>
          <dd>{formatDate(project.createdAt)}</dd>
        </div>
        <div className={projectDetailRowClassName}>
          <dt className="text-muted-foreground">Last updated</dt>
          <dd>{formatDate(project.updatedAt)}</dd>
        </div>
      </dl>

      {copyStatus === "error" ? (
        <p className="border-b border-destructive/30 bg-destructive/5 px-6 py-3 text-[10px] text-destructive">
          Unable to access the clipboard. Copy the project ID manually.
        </p>
      ) : null}

      <section className="grid gap-3 px-6 py-5">
        <h4 className="text-[10px] tracking-[0.15em] text-destructive uppercase">
          Danger zone
        </h4>
        <div className="flex items-center justify-between border border-destructive/25 bg-card px-4 py-3">
          <div className="min-w-0">
            <p className="text-xs text-foreground">Delete this project</p>
            <p className="mt-0.5 text-[10px] text-muted-foreground">
              Permanently remove its resources and owned volumes.
            </p>
          </div>
          <ProjectDeleteDialog
            onDeleted={onDeleted}
            project={project}
            trigger="button"
          />
        </div>
      </section>
    </div>
  );
};

export const ProjectSettingsDialog = ({
  onDeleted,
  project,
}: {
  onDeleted: (projectID: string) => void;
  project: Project;
}) => {
  const [section, setSection] = useState<ProjectSettingsSection>("general");
  const content = (() => {
    if (section === "general") {
      return <ProjectGeneralSettings onDeleted={onDeleted} project={project} />;
    }
    if (section === "usage") {
      return <ProjectUsage projectID={project.id} />;
    }
    if (section === "audit") {
      return (
        <AuditEventsView
          embedded
          projectId={project.id}
          projectName={project.name}
        />
      );
    }
    return <ProjectWebhooksSettings projectID={project.id} />;
  })();

  return (
    <Dialog.Root>
      <Dialog.Trigger
        render={
          <Button
            aria-label="Project settings"
            className="size-7"
            size="icon"
            title="Project settings"
            variant="ghost"
          >
            <Settings />
          </Button>
        }
      />
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
        <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
          <Dialog.Popup className="flex h-[min(42rem,calc(100dvh-2rem))] w-full max-w-7xl flex-col border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out">
            <header className="flex min-h-14 items-center gap-4 border-b border-border px-5 py-3">
              <div className="min-w-0">
                <Dialog.Title className="truncate text-sm font-medium">
                  Project settings
                </Dialog.Title>
                <Dialog.Description className="mt-1 truncate text-[9px] text-muted-foreground">
                  {project.name}
                </Dialog.Description>
              </div>
              <Dialog.Close
                aria-label="Close project settings"
                className="ml-auto grid size-8 shrink-0 place-items-center text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring"
              >
                <X className="size-4" />
              </Dialog.Close>
            </header>

            <div className="grid min-h-0 flex-1 md:grid-cols-[11rem_minmax(0,1fr)]">
              <nav
                aria-label="Project settings sections"
                className="flex gap-1 overflow-x-auto border-b border-border bg-muted/10 p-2 md:block md:overflow-visible md:border-r md:border-b-0"
              >
                {projectSettingsSections.map((item) => {
                  const Icon = item.icon;
                  return (
                    <button
                      className={cn(
                        "flex h-9 shrink-0 items-center gap-2 border-b-2 px-3 text-left text-[10px] transition-colors outline-none focus-visible:ring-1 focus-visible:ring-ring md:w-full md:border-b-0 md:border-l-2",
                        section === item.value
                          ? "border-foreground bg-muted text-foreground"
                          : "border-transparent text-muted-foreground hover:bg-muted/50 hover:text-foreground"
                      )}
                      key={item.value}
                      onClick={() => setSection(item.value)}
                      type="button"
                    >
                      <Icon className="size-3.5" />
                      {item.label}
                    </button>
                  );
                })}
              </nav>

              <div className="min-h-0 overflow-auto">{content}</div>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
};
