import { Menu } from "@base-ui/react/menu";
import {
  ArchiveRestore,
  Box,
  ChevronRight,
  FileClock,
  FolderKanban,
  KeyRound,
  Network,
  PackageOpen,
  Plus,
  Settings,
  ShieldCheck,
} from "lucide-react";
import { useState } from "react";
import type { ComponentType } from "react";
import { NavLink, useNavigate } from "react-router";

import type { Identity, Project } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ThemeMenuItems } from "@/theme-switcher";

export interface NavigationItem {
  icon: ComponentType<{ className?: string }>;
  label: string;
  path: string;
}

export const globalNavigation: NavigationItem[] = [
  { icon: FileClock, label: "Backups", path: "/backups" },
  { icon: PackageOpen, label: "Registry", path: "/registry" },
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
    "group flex items-center overflow-hidden px-2.5 py-2 text-xs transition-all duration-150",
    isActive
      ? "border-l-2 border-primary bg-secondary text-foreground"
      : "border-l-2 border-transparent text-muted-foreground hover:border-muted-foreground/30 hover:bg-secondary/50 hover:text-foreground"
  );

const sidebarLabelClassName = (collapsed: boolean, className?: string) =>
  cn(
    "min-w-0 overflow-hidden text-left whitespace-nowrap transition-[margin,max-width,opacity,transform] duration-200",
    collapsed
      ? "ml-0 max-w-0 -translate-x-1 opacity-0"
      : "ml-2.5 max-w-40 translate-x-0 opacity-100",
    className
  );

const identityInitials = (identity: Identity | null) => {
  const source = identity?.name ?? identity?.email ?? "Access user";
  return source
    .split(/[\s@._-]+/u)
    .filter(Boolean)
    .slice(0, 2)
    .map((part) => part.slice(0, 1))
    .join("")
    .toUpperCase();
};

const identitySubtitle = (
  identity: Identity | null,
  identityError: string | null
) => {
  if (identityError) {
    return "Identity unavailable";
  }
  if (identity?.name) {
    return identity.email ?? "Cloudflare Access";
  }
  return "Cloudflare Access";
};

const IdentityAvatar = ({ identity }: { identity: Identity | null }) => {
  const [failed, setFailed] = useState(false);
  if (identity?.avatarUrl && !failed) {
    return (
      <img
        alt=""
        className="size-7 shrink-0 border border-border object-cover"
        onError={() => setFailed(true)}
        referrerPolicy="no-referrer"
        src={identity.avatarUrl}
      />
    );
  }
  return (
    <span className="grid size-7 shrink-0 place-items-center border border-border bg-muted text-[9px] font-medium">
      {identityInitials(identity)}
    </span>
  );
};

const IdentityDetails = ({
  className,
  identity,
  identityError,
}: {
  className?: string;
  identity: Identity | null;
  identityError: string | null;
}) => {
  const title = identity?.name ?? identity?.email ?? "Access user";
  const subtitle = identitySubtitle(identity, identityError);
  return (
    <div className={cn("min-w-0", className)}>
      <div className="truncate text-[10px] font-medium">{title}</div>
      <div className="mt-0.5 truncate text-[9px] text-muted-foreground">
        {subtitle}
      </div>
    </div>
  );
};

const SidebarFooter = ({
  collapsed,
  identity,
  identityError,
  onCollapsedChange,
}: {
  collapsed: boolean;
  identity: Identity | null;
  identityError: string | null;
  onCollapsedChange: (collapsed: boolean) => void;
}) => (
  <div
    className={cn(
      "border-t border-border p-1.5",
      collapsed ? "grid place-items-center gap-1" : "flex items-center"
    )}
  >
    <Menu.Root>
      <Menu.Trigger
        aria-label="Open user menu"
        className={cn(
          "group flex items-center overflow-hidden text-xs transition-all duration-150 outline-none hover:bg-secondary/50 focus-visible:ring-1 focus-visible:ring-sidebar-ring",
          collapsed ? "size-9 p-1" : "min-w-0 flex-1 px-2.5 py-2"
        )}
      >
        <IdentityAvatar identity={identity} key={identity?.avatarUrl} />
        <IdentityDetails
          className={sidebarLabelClassName(collapsed)}
          identity={identity}
          identityError={identityError}
        />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          align="start"
          className="z-50"
          side="top"
          sideOffset={4}
        >
          <Menu.Popup className="w-52 border border-border bg-popover p-1 text-popover-foreground shadow-lg">
            <IdentityDetails
              className="border-b border-border px-2.5 py-2"
              identity={identity}
              identityError={identityError}
            />
            <ThemeMenuItems />
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>

    <button
      aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
      className="shrink-0 p-1.5 text-muted-foreground transition-colors duration-150 hover:text-foreground"
      onClick={() => onCollapsedChange(!collapsed)}
      type="button"
    >
      <span
        className={cn(
          "block transition-transform duration-200",
          collapsed ? "rotate-0" : "rotate-180"
        )}
      >
        <ChevronRight className="size-3.5" />
      </span>
    </button>
  </div>
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
        "relative flex shrink-0 flex-col overflow-hidden border-r border-border bg-card transition-[width] duration-200",
        collapsed ? "w-12" : "w-52"
      )}
    >
      <button
        className="flex h-12 items-center gap-2.5 border-b border-border px-3 text-left"
        onClick={() => navigate("/")}
        type="button"
      >
        <span className="grid size-7 shrink-0 place-items-center border border-border bg-secondary text-[10px] font-bold">
          pd
        </span>
        {!collapsed && (
          <span className="min-w-0 space-y-0.5">
            <span className="block text-xs leading-none font-semibold">
              platformd
            </span>
            <span className="block text-[9px] leading-none whitespace-nowrap text-muted-foreground">
              single-vps control plane
            </span>
          </span>
        )}
      </button>

      <nav className="min-h-0 flex-1 overflow-x-hidden overflow-y-auto p-1.5">
        {recovery ? (
          <NavLink
            className={navClassName}
            title={collapsed ? "Recovery" : undefined}
            to="/recovery"
          >
            <ArchiveRestore className="size-4 shrink-0" />
            <span className={sidebarLabelClassName(collapsed)}>Recovery</span>
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
                <span className={sidebarLabelClassName(collapsed)}>
                  Projects
                </span>
              </NavLink>
              {!collapsed && (
                <Button
                  aria-label="Create project"
                  className="size-7"
                  onClick={() => navigate("/projects/new")}
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
                <span className="shrink-0 transition-transform duration-150 group-hover:scale-110">
                  <Box className="size-3.5" />
                </span>
                <span
                  className={sidebarLabelClassName(
                    collapsed,
                    "max-w-32 truncate"
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
                  <span className="shrink-0 transition-transform duration-150 group-hover:scale-110">
                    <Icon className="size-4" />
                  </span>
                  <span className={sidebarLabelClassName(collapsed)}>
                    {item.label}
                  </span>
                </NavLink>
              );
            })}
          </>
        )}
      </nav>

      <SidebarFooter
        collapsed={collapsed}
        identity={identity}
        identityError={identityError}
        onCollapsedChange={onCollapsedChange}
      />
    </aside>
  );
};
