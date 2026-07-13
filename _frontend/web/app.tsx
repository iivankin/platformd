import { Boxes, TerminalSquare } from "lucide-react";
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
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { LogsPage } from "@/logs-page";
import { ProjectCanvasPage } from "@/project-canvas-page";
import { ProjectsPage } from "@/projects-page";
import { RegistryPage } from "@/registry-page";
import { globalNavigation, Sidebar } from "@/sidebar";
import type { NavigationItem } from "@/sidebar";

const pageDescriptions: Record<string, string> = {
  "/audit": "Administrative history retained for seven days.",
  "/backups": "Per-resource backup schedules, runs, and restores.",
  "/infrastructure": "Host, runtime, network, and disk pressure.",
  "/logs": "Deployment, managed resource, and job output.",
  "/registry": "OCI repositories, images, tags, and credentials.",
  "/tokens": "Scoped REST and MCP automation credentials.",
};

const EmptySection = ({ item }: { item: NavigationItem }) => {
  const Icon = item.icon;
  return (
    <section className="enter-row border-b border-border">
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
    <div className="enter-row">
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
            Projects contain isolated services, managed databases, object
            stores, and internal DNS names.
          </p>
          <Button className="mt-5" onClick={() => navigate("/projects")}>
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
  const activeLabel = useMemo(() => {
    if (location.pathname === "/") {
      return "Overview";
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
  }, [data.projects, location.pathname]);

  return (
    <div className="flex h-full bg-background text-foreground">
      <Sidebar
        collapsed={collapsed}
        identity={data.identity}
        identityError={data.identityError}
        onCollapsedChange={setCollapsed}
        projects={data.projects}
      />

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center justify-between border-b border-border px-5">
          <h1 className="truncate text-xs font-semibold tracking-[0.15em] uppercase">
            {activeLabel}
          </h1>
          <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
            <span
              className={cn(
                "size-1.5 bg-emerald-500",
                (data.metaError || !data.meta) && "bg-amber-500"
              )}
            />
            {data.metaError
              ? "control plane unavailable"
              : (data.meta?.status ?? "connecting")}
            <TerminalSquare className="ml-2 size-3.5" />
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-auto">
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
                  onCreated={data.handleProjectCreated}
                  projects={data.projects}
                />
              }
              path="/projects"
            />
            <Route
              element={<ProjectCanvasPage />}
              path="/projects/:projectID"
            />
            <Route
              element={<APITokensPage projects={data.projects} />}
              path="/tokens"
            />
            <Route
              element={<LogsPage projects={data.projects} />}
              path="/logs"
            />
            <Route element={<AuditPage />} path="/audit" />
            <Route element={<RegistryPage />} path="/registry" />
            {globalNavigation
              .filter(
                (item) =>
                  item.path !== "/tokens" &&
                  item.path !== "/logs" &&
                  item.path !== "/audit" &&
                  item.path !== "/registry"
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
        </div>
      </main>
    </div>
  );
};
