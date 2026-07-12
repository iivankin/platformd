import {
  Activity,
  Boxes,
  Braces,
  ChevronLeft,
  Container,
  Database,
  FileClock,
  FolderKanban,
  HardDrive,
  KeyRound,
  Logs,
  Network,
  PackageOpen,
  Settings,
  ShieldCheck,
  TerminalSquare,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type { ComponentType } from "react";
import {
  NavLink,
  Navigate,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from "react-router";

import { fetchMeta } from "@/api";
import type { Meta } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ProjectsPage } from "@/projects-page";

interface NavigationItem {
  icon: ComponentType<{ className?: string }>;
  label: string;
  path: string;
}

const overviewNavigation: NavigationItem = {
  icon: Activity,
  label: "Overview",
  path: "/",
};

const navigation: NavigationItem[] = [
  overviewNavigation,
  { icon: FolderKanban, label: "Projects", path: "/projects" },
  { icon: Container, label: "Services", path: "/services" },
  { icon: Database, label: "PostgreSQL", path: "/postgres" },
  { icon: Braces, label: "Redis", path: "/redis" },
  { icon: HardDrive, label: "Object Storage", path: "/objects" },
  { icon: PackageOpen, label: "Registry", path: "/registry" },
  { icon: Logs, label: "Logs", path: "/logs" },
  { icon: FileClock, label: "Backups", path: "/backups" },
  { icon: KeyRound, label: "API Tokens", path: "/tokens" },
  { icon: Network, label: "Infrastructure", path: "/infrastructure" },
  { icon: ShieldCheck, label: "Audit", path: "/audit" },
];

const pageDescriptions: Record<string, string> = {
  "/": "Installation health and recent platform activity.",
  "/audit": "Administrative history retained for seven days.",
  "/backups": "Resource backup health across the installation.",
  "/infrastructure": "Host, runtime, network, and disk pressure.",
  "/logs": "Deployment, managed resource, and job output.",
  "/objects": "Private S3-compatible stores and object data.",
  "/postgres": "Managed PostgreSQL instances and SQL consoles.",
  "/projects": "Isolation boundaries for services and data.",
  "/redis": "Managed Redis instances and key browsers.",
  "/registry": "OCI repositories, images, tags, and credentials.",
  "/services": "OCI deployments, variables, volumes, and domains.",
  "/tokens": "Scoped REST and MCP automation credentials.",
};

const useMeta = (): { error: string | null; meta: Meta | null } => {
  const [meta, setMeta] = useState<Meta | null>(null);
  const [metaError, setMetaError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const loadMeta = async () => {
      try {
        setMeta(await fetchMeta(controller.signal));
      } catch (error) {
        if (error instanceof DOMException && error.name === "AbortError") {
          return;
        }
        setMetaError(
          error instanceof Error ? error.message : "meta request failed"
        );
      }
    };
    void loadMeta();
    return () => controller.abort();
  }, []);

  return { error: metaError, meta };
};

const EmptySection = ({ item }: { item: NavigationItem }) => {
  const Icon = item.icon;
  const description = pageDescriptions[item.path] ?? "";

  return (
    <section className="enter-row border-b border-border">
      <div className="grid min-h-72 place-items-center px-8 py-16 text-center">
        <div className="max-w-md">
          <Icon className="mx-auto mb-5 size-6 text-muted-foreground" />
          <h2 className="text-sm font-medium">{item.label}</h2>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            {description}
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
}: {
  error: string | null;
  meta: Meta | null;
}) => {
  const navigate = useNavigate();
  const facts = [
    ["Control plane", error ? "unreachable" : (meta?.status ?? "connecting")],
    ["Version", meta?.version ?? "—"],
    ["Runtime", meta ? `${meta.os}/${meta.architecture}` : "—"],
    ["Disk pressure", "normal"],
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
          <h2 className="text-sm font-medium">Create the first project</h2>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">
            Projects contain isolated services, managed databases, object
            stores, and internal DNS names.
          </p>
          <Button className="mt-5" onClick={() => navigate("/projects")}>
            New project
          </Button>
        </div>
      </section>
    </div>
  );
};

export const App = () => {
  const location = useLocation();
  const { error, meta } = useMeta();
  const [collapsed, setCollapsed] = useState(false);
  const activeItem = useMemo(
    () =>
      navigation.find((item) =>
        item.path === "/"
          ? location.pathname === "/"
          : location.pathname.startsWith(item.path)
      ) ?? overviewNavigation,
    [location.pathname]
  );

  return (
    <div className="flex h-full bg-background text-foreground">
      <aside
        className={cn(
          "flex shrink-0 flex-col border-r border-border bg-sidebar transition-[width] duration-200",
          collapsed ? "w-12" : "w-56"
        )}
      >
        <div className="flex h-12 items-center border-b border-border px-3">
          <div className="grid size-7 shrink-0 place-items-center border border-border bg-secondary text-[10px] font-bold">
            pd
          </div>
          <div
            className={cn(
              "ml-2.5 min-w-0 overflow-hidden transition-opacity",
              collapsed && "opacity-0"
            )}
          >
            <div className="text-xs leading-none font-semibold">platformd</div>
            <div className="mt-1 text-[9px] leading-none whitespace-nowrap text-muted-foreground">
              single-vps control plane
            </div>
          </div>
        </div>

        <nav className="min-h-0 flex-1 overflow-y-auto p-1.5">
          {navigation.map((item) => {
            const Icon = item.icon;
            return (
              <NavLink
                className={({ isActive }) =>
                  cn(
                    "group flex h-8 items-center overflow-hidden border-l-2 border-transparent px-2.5 text-xs transition-colors",
                    isActive
                      ? "border-sidebar-primary bg-sidebar-accent text-sidebar-accent-foreground"
                      : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground"
                  )
                }
                end={item.path === "/"}
                key={item.path}
                title={collapsed ? item.label : undefined}
                to={item.path}
              >
                <Icon className="size-4 shrink-0" />
                <span
                  className={cn(
                    "ml-2.5 whitespace-nowrap transition-opacity",
                    collapsed && "opacity-0"
                  )}
                >
                  {item.label}
                </span>
              </NavLink>
            );
          })}
        </nav>

        <div
          className={cn(
            "flex items-center border-t border-border p-1.5",
            collapsed && "justify-center"
          )}
        >
          {!collapsed && (
            <Button className="min-w-0 flex-1 justify-start" variant="ghost">
              <Settings />
              <span className="whitespace-nowrap">Settings</span>
            </Button>
          )}
          <Button
            aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
            onClick={() => setCollapsed((value) => !value)}
            size="icon"
            variant="ghost"
          >
            <ChevronLeft className={cn(collapsed && "rotate-180")} />
          </Button>
        </div>
      </aside>

      <main className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-12 shrink-0 items-center justify-between border-b border-border px-5">
          <div>
            <h1 className="text-xs font-semibold tracking-[0.15em] uppercase">
              {activeItem.label}
            </h1>
          </div>
          <div className="flex items-center gap-2 text-[10px] text-muted-foreground">
            <span
              className={cn(
                "size-1.5 bg-emerald-500",
                (error || !meta) && "bg-amber-500"
              )}
            />
            {error
              ? "control plane unavailable"
              : (meta?.status ?? "connecting")}
            <TerminalSquare className="ml-2 size-3.5" />
          </div>
        </header>

        <div className="min-h-0 flex-1 overflow-auto">
          <Routes>
            <Route element={<Overview error={error} meta={meta} />} path="/" />
            <Route element={<ProjectsPage />} path="/projects/*" />
            {navigation
              .slice(1)
              .filter((item) => item.path !== "/projects")
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
