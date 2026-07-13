import {
  ArchiveRestore,
  Box,
  ChevronLeft,
  FileClock,
  FolderKanban,
  KeyRound,
  Logs,
  Network,
  PackageOpen,
  Plus,
  Settings,
  ShieldCheck,
  UserRound,
} from "lucide-react";
import type { ComponentType } from "react";
import { NavLink, useNavigate } from "react-router";

import type { Identity, Project } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

export interface NavigationItem {
  icon: ComponentType<{ className?: string }>;
  label: string;
  path: string;
}

export const globalNavigation: NavigationItem[] = [
  { icon: FileClock, label: "Backups", path: "/backups" },
  { icon: PackageOpen, label: "Registry", path: "/registry" },
  { icon: Logs, label: "Logs", path: "/logs" },
  { icon: KeyRound, label: "API Tokens", path: "/tokens" },
  { icon: Network, label: "Infrastructure", path: "/infrastructure" },
  { icon: ShieldCheck, label: "Audit", path: "/audit" },
  { icon: Settings, label: "Settings", path: "/settings" },
];

interface SidebarProperties {
  collapsed: boolean;
  identity: Identity | null;
  identityError: string | null;
  onCollapsedChange: (collapsed: boolean) => void;
  projects: Project[];
  recovery?: boolean;
}

const navClassName = ({ isActive }: { isActive: boolean }) =>
  cn(
    "group flex h-8 items-center overflow-hidden border-l-2 border-transparent px-2.5 text-xs transition-colors",
    isActive
      ? "border-sidebar-primary bg-sidebar-accent text-sidebar-accent-foreground"
      : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground"
  );

export const Sidebar = ({
  collapsed,
  identity,
  identityError,
  onCollapsedChange,
  projects,
  recovery = false,
}: SidebarProperties) => {
  const navigate = useNavigate();

  return (
    <aside
      className={cn(
        "flex shrink-0 flex-col border-r border-border bg-sidebar transition-[width] duration-200",
        collapsed ? "w-12" : "w-56"
      )}
    >
      <button
        className="flex h-12 items-center border-b border-border px-3 text-left"
        onClick={() => navigate("/")}
        type="button"
      >
        <span className="grid size-7 shrink-0 place-items-center border border-border bg-secondary text-[10px] font-bold">
          pd
        </span>
        <span
          className={cn(
            "ml-2.5 min-w-0 overflow-hidden transition-opacity",
            collapsed && "opacity-0"
          )}
        >
          <span className="block text-xs leading-none font-semibold">
            platformd
          </span>
          <span className="mt-1 block text-[9px] leading-none whitespace-nowrap text-muted-foreground">
            single-vps control plane
          </span>
        </span>
      </button>

      <nav className="min-h-0 flex-1 overflow-y-auto p-1.5">
        {recovery ? (
          <NavLink
            className={navClassName}
            title={collapsed ? "Recovery" : undefined}
            to="/recovery"
          >
            <ArchiveRestore className="size-4 shrink-0" />
            <span
              className={cn(
                "ml-2.5 whitespace-nowrap transition-opacity",
                collapsed && "opacity-0"
              )}
            >
              Recovery
            </span>
          </NavLink>
        ) : (
          <>
            <div className="flex h-8 items-center">
              <NavLink
                className={({ isActive }) =>
                  cn(navClassName({ isActive }), "min-w-0 flex-1")
                }
                end
                title={collapsed ? "Projects" : undefined}
                to="/projects"
              >
                <FolderKanban className="size-4 shrink-0" />
                <span
                  className={cn(
                    "ml-2.5 whitespace-nowrap transition-opacity",
                    collapsed && "opacity-0"
                  )}
                >
                  Projects
                </span>
              </NavLink>
              {!collapsed && (
                <Button
                  aria-label="Create project"
                  className="size-7"
                  onClick={() => navigate("/projects?new=1")}
                  size="icon"
                  title="Create project"
                  variant="ghost"
                >
                  <Plus />
                </Button>
              )}
            </div>

            {projects.map((project) => (
              <NavLink
                className={({ isActive }) =>
                  cn(navClassName({ isActive }), !collapsed && "pl-6")
                }
                key={project.id}
                title={collapsed ? project.name : undefined}
                to={`/projects/${project.id}`}
              >
                <Box className="size-3.5 shrink-0" />
                <span
                  className={cn(
                    "ml-2 truncate whitespace-nowrap transition-opacity",
                    collapsed && "opacity-0"
                  )}
                >
                  {project.name}
                </span>
              </NavLink>
            ))}

            <div className="my-1.5 border-t border-border" />
            {globalNavigation.map((item) => {
              const Icon = item.icon;
              return (
                <NavLink
                  className={navClassName}
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
          </>
        )}
      </nav>

      <div className="border-t border-border p-1.5">
        {!collapsed && (
          <div className="flex min-w-0 items-center px-2.5 py-2">
            <UserRound className="size-4 shrink-0 text-muted-foreground" />
            <div className="ml-2.5 min-w-0">
              <div className="truncate text-[10px] font-medium">
                {identity?.email ?? "Access user"}
              </div>
              <div className="mt-0.5 truncate text-[9px] text-muted-foreground">
                {identityError ? "identity unavailable" : "Cloudflare Access"}
              </div>
            </div>
          </div>
        )}
        <div className={cn("flex items-center", collapsed && "justify-center")}>
          <Button
            aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
            onClick={() => onCollapsedChange(!collapsed)}
            size="icon"
            variant="ghost"
          >
            <ChevronLeft className={cn(collapsed && "rotate-180")} />
          </Button>
        </div>
      </div>
    </aside>
  );
};
