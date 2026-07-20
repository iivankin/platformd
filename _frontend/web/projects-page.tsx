import {
  Boxes,
  Database,
  HardDrive,
  Network,
  Plus,
  Server,
} from "lucide-react";
import { Link } from "react-router";

import type { Project } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";

const dateFormatter = new Intl.DateTimeFormat(undefined, {
  day: "2-digit",
  month: "short",
  year: "numeric",
});

const resourceCount = (project: Project) =>
  project.serviceCount +
  project.postgresCount +
  project.redisCount +
  project.objectStoreCount +
  project.networkGatewayCount;

interface ProjectsPageProperties {
  loadError: string | null;
  loading: boolean;
  projects: Project[];
}

export const ProjectsPage = ({
  loadError,
  loading,
  projects,
}: ProjectsPageProperties) => (
  <PageStack className="animate-in duration-200 fade-in slide-in-from-bottom-1">
    <SectionCard className="flex min-h-14 items-center justify-between gap-4 px-5 py-3">
      <div>
        <p className="text-xs font-medium">Projects</p>
        <p className="mt-1 text-[10px] text-muted-foreground">
          {projects.length} project{projects.length === 1 ? "" : "s"} ·{" "}
          {projects.reduce(
            (total, project) => total + resourceCount(project),
            0
          )}{" "}
          resources
        </p>
      </div>
      <Button
        nativeButton={false}
        render={<Link to="/projects/new" />}
        size="sm"
      >
        <Plus />
        New project
      </Button>
    </SectionCard>

    {loadError ? (
      <SectionCard className="bg-destructive/5 px-5 py-4 text-xs text-destructive ring-destructive/30">
        {loadError}
      </SectionCard>
    ) : null}

    {loading ? (
      <SectionCard className="px-5 py-10 text-center text-[10px] tracking-[0.14em] text-muted-foreground uppercase">
        Loading projects
      </SectionCard>
    ) : null}

    {!loading && !loadError && projects.length === 0 ? (
      <SectionCard className="grid min-h-80 place-items-center px-8 py-16 text-center">
        <div className="max-w-md">
          <Boxes className="mx-auto size-7 text-muted-foreground" />
          <h2 className="mt-5 text-sm font-medium">No projects yet</h2>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            Create a project to group the services and data for an application.
          </p>
          <Button
            className="mt-5"
            nativeButton={false}
            render={<Link to="/projects/new" />}
          >
            <Plus />
            Create project
          </Button>
        </div>
      </SectionCard>
    ) : null}

    {projects.length > 0 ? (
      <SectionCard>
        <div className="grid grid-cols-[minmax(180px,1.3fr)_minmax(240px,1fr)_100px] border-b border-border bg-muted/30 px-5 py-2 text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          <span>Project</span>
          <span>Resources</span>
          <span>Updated</span>
        </div>
        <div className="[content-visibility:auto]">
          {projects.map((project) => (
            <Link
              className="grid min-h-14 grid-cols-[minmax(180px,1.3fr)_minmax(240px,1fr)_100px] items-center border-b border-border px-5 py-3 last:border-b-0 hover:bg-muted/25"
              key={project.id}
              to={`/projects/${project.id}`}
            >
              <div className="min-w-0">
                <div className="truncate text-xs font-medium">
                  {project.name}
                </div>
                <div className="mt-1 truncate text-[9px] text-muted-foreground">
                  {project.name}.internal
                </div>
              </div>
              <div className="flex items-center gap-4 text-[10px] text-muted-foreground">
                <span className="flex items-center gap-1.5" title="Services">
                  <Server className="size-3" />
                  {project.serviceCount}
                </span>
                <span
                  className="flex items-center gap-1.5"
                  title="PostgreSQL and Redis"
                >
                  <Database className="size-3" />
                  {project.postgresCount + project.redisCount}
                </span>
                <span
                  className="flex items-center gap-1.5"
                  title="Object stores"
                >
                  <HardDrive className="size-3" />
                  {project.objectStoreCount}
                </span>
                <span
                  className="flex items-center gap-1.5"
                  title="Network gateways"
                >
                  <Network className="size-3" />
                  {project.networkGatewayCount}
                </span>
              </div>
              <time
                className="text-[9px] text-muted-foreground"
                dateTime={new Date(project.updatedAt).toISOString()}
              >
                {dateFormatter.format(project.updatedAt)}
              </time>
            </Link>
          ))}
        </div>
      </SectionCard>
    ) : null}
  </PageStack>
);
