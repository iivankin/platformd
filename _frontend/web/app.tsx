import { Boxes } from "lucide-react";
import { useMemo, useState } from "react";
import {
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router";

import type { Meta } from "@/api";
import { APITokensPage } from "@/api-tokens-page";
import { useAppData } from "@/app-data";
import { AuditPage } from "@/audit-page";
import { BackupsPage } from "@/backups-page";
import { Button } from "@/components/ui/button";
import { InfrastructurePage } from "@/infrastructure-page";
import { cn } from "@/lib/utils";
import { ProjectCanvasPage } from "@/project-canvas-page";
import { ProjectChangesProvider } from "@/project-changes";
import { ProjectCreatePage } from "@/project-create-page";
import { ProjectsPage } from "@/projects-page";
import { RecoveryPage } from "@/recovery-page";
import { RegistryPage } from "@/registry-page";
import { SettingsPage } from "@/settings-page";
import { globalNavigation, Sidebar } from "@/sidebar";
import type { NavigationItem } from "@/sidebar";
import { useLastProject } from "@/use-last-project";

const pageDescriptions: Record<string, string> = {
  "/audit": "Administrative history retained for seven days.",
  "/backups": "Backup schedules, restore points, and storage.",
  "/infrastructure": "Server health, maintenance, and platform activity.",
  "/registry": "Private images used by your services.",
  "/settings": "Installation access and secure hostnames.",
  "/tokens": "Scoped REST and MCP automation credentials.",
};

const controlPlaneStatusLabel = (
  status: Meta["status"] | undefined,
  unavailable: boolean
) => {
  if (unavailable) {
    return "Control plane unavailable";
  }
  if (status === "ready") {
    return "Control plane ready";
  }
  if (status === "bootstrapping") {
    return "Control plane starting";
  }
  if (status === "recovery") {
    return "Recovery mode";
  }
  return "Connecting";
};

const EmptySection = ({ item }: { item: NavigationItem }) => {
  const Icon = item.icon;
  return (
    <section className="animate-in border-b border-border duration-200 fade-in slide-in-from-bottom-1">
      <div className="grid min-h-72 place-items-center px-8 py-16 text-center">
        <div className="max-w-md">
          <Icon className="mx-auto mb-5 size-6 text-muted-foreground" />
          <h2 className="text-sm font-medium">{item.label}</h2>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            {pageDescriptions[item.path] ?? ""}
          </p>
          <div className="mx-auto mt-6 h-px w-16 bg-border" />
          <p className="mt-4 text-[10px] tracking-[0.14em] text-muted-foreground uppercase">
            No resources yet
          </p>
        </div>
      </div>
    </section>
  );
};

const Overview = ({
  error,
  meta,
  projectCount,
}: {
  error: string | null;
  meta: Meta | null;
  projectCount: number;
}) => {
  const navigate = useNavigate();
  const facts = [
    ["Control plane", error ? "unreachable" : (meta?.status ?? "connecting")],
    ["Version", meta?.version ?? "—"],
    ["Runtime", meta ? `${meta.os}/${meta.architecture}` : "—"],
    ["Projects", projectCount.toString()],
  ];

  return (
    <div className="animate-in duration-200 fade-in slide-in-from-bottom-1">
      <section className="grid border-b border-border md:grid-cols-4">
        {facts.map(([label, value]) => (
          <div
            className="border-b border-border px-5 py-4 last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0"
            key={label}
          >
            <div className="text-[10px] tracking-[0.12em] text-muted-foreground uppercase">
              {label}
            </div>
            <div className="mt-2 truncate text-xs font-medium">{value}</div>
          </div>
        ))}
      </section>
      <section className="grid min-h-80 place-items-center border-b border-border px-8 py-16 text-center">
        <div className="max-w-lg">
          <Boxes className="mx-auto mb-5 size-7 text-muted-foreground" />
          <h2 className="text-sm font-medium">
            {projectCount === 0
              ? "Create the first project"
              : "Open a project canvas"}
          </h2>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            Projects group services, databases, storage, and their
            configuration.
          </p>
          <Button
            className="mt-5"
            onClick={() =>
              navigate(projectCount === 0 ? "/projects/new" : "/projects")
            }
          >
            {projectCount === 0 ? "New project" : "View projects"}
          </Button>
        </div>
      </section>
    </div>
  );
};

export const App = () => {
  const location = useLocation();
  const [collapsed, setCollapsed] = useState(false);
  const data = useAppData();
  useLastProject(data.projects, data.projectsLoading);
  const activeLabel = useMemo(() => {
    if (data.meta?.status === "recovery") {
      return "Recovery";
    }
    if (location.pathname === "/") {
      return "Overview";
    }
    if (location.pathname === "/projects/new") {
      return "New project";
    }
    if (location.pathname === "/projects") {
      return "Projects";
    }
    if (location.pathname.startsWith("/projects/")) {
      const [projectID] = location.pathname
        .slice("/projects/".length)
        .split("/");
      return (
        data.projects.find((project) => project.id === projectID)?.name ??
        "Project"
      );
    }
    return (
      globalNavigation.find((item) => location.pathname.startsWith(item.path))
        ?.label ?? "platformd"
    );
  }, [data.meta?.status, data.projects, location.pathname]);

  const recovering = data.meta?.status === "recovery";
  const controlPlaneReady = data.meta?.status === "ready";
  const controlPlaneStatus = controlPlaneStatusLabel(
    data.meta?.status,
    Boolean(data.metaError)
  );

  return (
    <ProjectChangesProvider>
      <div className="flex h-full bg-background text-foreground">
        <Sidebar
          collapsed={collapsed}
          identity={data.identity}
          identityError={data.identityError}
          onCollapsedChange={setCollapsed}
          projects={data.projects}
          recovery={recovering}
          updateAvailable={Boolean(data.update.status?.updateAvailable)}
        />

        <main className="relative flex min-w-0 flex-1 flex-col">
          <header className="flex h-12 shrink-0 items-center justify-between border-b border-border px-5">
            <h1 className="truncate text-xs font-semibold tracking-[0.15em] uppercase">
              {activeLabel}
            </h1>
            <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
              <span
                className={cn(
                  "size-1.5 bg-emerald-500",
                  !controlPlaneReady && "bg-amber-500"
                )}
              />
              {controlPlaneStatus}
            </div>
          </header>

          <div className="min-h-0 flex-1 overflow-auto">
            {recovering ? (
              <Routes>
                <Route element={<RecoveryPage />} path="/recovery" />
                <Route element={<Navigate replace to="/recovery" />} path="*" />
              </Routes>
            ) : (
              <Routes>
                <Route
                  element={
                    <Overview
                      error={data.metaError}
                      meta={data.meta}
                      projectCount={data.projects.length}
                    />
                  }
                  path="/"
                />
                <Route
                  element={
                    <ProjectsPage
                      loadError={data.projectsError}
                      loading={data.projectsLoading}
                      projects={data.projects}
                    />
                  }
                  path="/projects"
                />
                <Route
                  element={
                    <ProjectCreatePage onCreated={data.handleProjectCreated} />
                  }
                  path="/projects/new"
                />
                <Route
                  element={
                    <ProjectCanvasPage
                      onProjectDeleted={data.handleProjectDeleted}
                    />
                  }
                  path="/projects/:projectID/:resourceCollection/:resourceID/deployments/:deploymentID/:deploymentView?"
                />
                <Route
                  element={
                    <ProjectCanvasPage
                      onProjectDeleted={data.handleProjectDeleted}
                    />
                  }
                  path="/projects/:projectID/:resourceCollection/:resourceID/:view?"
                />
                <Route
                  element={
                    <ProjectCanvasPage
                      onProjectDeleted={data.handleProjectDeleted}
                    />
                  }
                  path="/projects/:projectID"
                />
                <Route
                  element={<APITokensPage projects={data.projects} />}
                  path="/tokens"
                />
                <Route
                  element={<InfrastructurePage update={data.update} />}
                  path="/infrastructure/*"
                />
                <Route element={<AuditPage />} path="/audit" />
                <Route element={<BackupsPage />} path="/backups/*" />
                <Route element={<RegistryPage />} path="/registry/*" />
                <Route element={<SettingsPage />} path="/settings/*" />
                {globalNavigation
                  .filter(
                    (item) =>
                      item.path !== "/tokens" &&
                      item.path !== "/audit" &&
                      item.path !== "/backups" &&
                      item.path !== "/registry" &&
                      item.path !== "/infrastructure" &&
                      item.path !== "/settings"
                  )
                  .map((item) => (
                    <Route
                      element={<EmptySection item={item} />}
                      key={item.path}
                      path={`${item.path}/*`}
                    />
                  ))}
                <Route element={<Navigate replace to="/" />} path="*" />
              </Routes>
            )}
          </div>
        </main>
      </div>
    </ProjectChangesProvider>
  );
};
