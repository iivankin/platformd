import { Menu } from "@base-ui/react/menu";
import {
  Activity,
  ArchiveRestore,
  Box,
  ChevronRight,
  FolderKanban,
  LogOut,
  Plus,
  Settings,
} from "lucide-react";
import { useState } from "react";
import type { ComponentType } from "react";
import { NavLink, useNavigate } from "react-router";

import type { Identity, Project } from "@/api";
import { projectIconURL } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ThemeMenuItems } from "@/theme-switcher";

export interface NavigationItem {
  icon: ComponentType<{ className?: string }>;
  label: string;
  path: string;
}

export const globalNavigation: NavigationItem[] = [
  { icon: Activity, label: "Monitoring", path: "/monitoring" },
  { icon: Settings, label: "Settings", path: "/settings" },
];

interface SidebarProperties {
  collapsed: boolean;
  identity: Identity | null;
  identityError: string | null;
  identityLoading: boolean;
  mobile?: boolean;
  onCollapsedChange: (collapsed: boolean) => void;
  projects: Project[];
  recovery?: boolean;
  updateAvailable?: boolean;
}

const ProjectNavIcon = ({
  labelsCollapsed,
  project,
}: {
  labelsCollapsed: boolean;
  project: Project;
}) => {
  if (project.hasIcon) {
    return (
      <img
        alt=""
        className="size-3.5 object-cover"
        src={projectIconURL(project)}
      />
    );
  }
  if (labelsCollapsed) {
    return (
      <span className="grid size-3.5 place-items-center text-[9px] leading-none font-medium">
        {project.name.slice(0, 1).toUpperCase()}
      </span>
    );
  }
  return <Box className="size-3.5" />;
};

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

const identityTitle = (
  identity: Identity | null,
  identityError: string | null
) => {
  if (identityError && !identity) {
    return "Identity unavailable";
  }
  return identity?.name ?? identity?.email ?? "Access user";
};

const identitySubtitle = (
  identity: Identity | null,
  identityError: string | null
) => {
  if (identityError && !identity) {
    return null;
  }
  if (identity?.name) {
    return identity.email ?? null;
  }
  return null;
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
  const title = identityTitle(identity, identityError);
  const subtitle = identitySubtitle(identity, identityError);
  return (
    <div className={cn("min-w-0", className)}>
      <div className="truncate text-[10px] font-medium">{title}</div>
      {subtitle ? (
        <div className="mt-0.5 truncate text-[9px] text-muted-foreground">
          {subtitle}
        </div>
      ) : null}
    </div>
  );
};

const accessLogout = () => {
  window.location.assign("/cdn-cgi/access/logout");
};

const SidebarFooter = ({
  collapsed,
  identity,
  identityError,
  identityLoading,
  onCollapsedChange,
}: {
  collapsed: boolean;
  identity: Identity | null;
  identityError: string | null;
  identityLoading: boolean;
  onCollapsedChange: (collapsed: boolean) => void;
}) => (
  <div
    className={cn(
      "border-t border-border p-1.5",
      collapsed ? "grid place-items-center gap-1" : "flex items-center"
    )}
  >
    {identityLoading ? (
      <div
        aria-hidden="true"
        className={collapsed ? "size-9" : "h-11 min-w-0 flex-1"}
      />
    ) : (
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
              <Menu.Separator className="my-1 h-px bg-border" />
              <Menu.Item
                className="flex cursor-default items-center gap-2 px-2.5 py-2 text-[10px] text-muted-foreground outline-none data-[highlighted]:bg-muted data-[highlighted]:text-foreground"
                onClick={accessLogout}
              >
                <LogOut className="size-3.5" />
                Log out
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
    )}

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
  identityLoading,
  mobile = false,
  onCollapsedChange,
  projects,
  recovery = false,
  updateAvailable = false,
}: SidebarProperties) => {
  const navigate = useNavigate();
  const overlay = mobile && !collapsed;
  const labelsCollapsed = collapsed && !overlay;

  return (
    <>
      {overlay ? <div aria-hidden="true" className="w-12 shrink-0" /> : null}
      <aside
        className={cn(
          "flex shrink-0 flex-col overflow-hidden border-r border-border bg-card transition-[width] duration-200",
          overlay
            ? "fixed inset-y-0 left-0 z-40 w-52 shadow-2xl"
            : cn("relative", collapsed ? "w-12" : "w-52")
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
          {!labelsCollapsed && (
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
              title={labelsCollapsed ? "Recovery" : undefined}
              to="/recovery"
            >
              <ArchiveRestore className="size-4 shrink-0" />
              <span className={sidebarLabelClassName(labelsCollapsed)}>
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
                  title={labelsCollapsed ? "Projects" : undefined}
                  to="/projects"
                >
                  <FolderKanban className="size-4 shrink-0" />
                  <span className={sidebarLabelClassName(labelsCollapsed)}>
                    Projects
                  </span>
                </NavLink>
                {!labelsCollapsed && (
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
                    cn(navClassName({ isActive }), !labelsCollapsed && "pl-6")
                  }
                  key={project.id}
                  title={labelsCollapsed ? project.name : undefined}
                  to={`/projects/${project.id}`}
                >
                  <span className="shrink-0 transition-transform duration-150 group-hover:scale-110">
                    <ProjectNavIcon
                      labelsCollapsed={labelsCollapsed}
                      project={project}
                    />
                  </span>
                  <span
                    className={sidebarLabelClassName(
                      labelsCollapsed,
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
                const showUpdate =
                  item.path === "/monitoring" && updateAvailable;
                return (
                  <NavLink
                    aria-label={
                      showUpdate
                        ? `${item.label}, update available`
                        : item.label
                    }
                    className={({ isActive }) =>
                      cn(navClassName({ isActive }), "relative")
                    }
                    key={item.path}
                    title={labelsCollapsed ? item.label : undefined}
                    to={item.path}
                  >
                    <span className="shrink-0 transition-transform duration-150 group-hover:scale-110">
                      <Icon className="size-4" />
                    </span>
                    <span className={sidebarLabelClassName(labelsCollapsed)}>
                      {item.label}
                    </span>
                    {showUpdate ? (
                      <span
                        aria-hidden="true"
                        className={cn(
                          "shrink-0 bg-cyan-500",
                          labelsCollapsed
                            ? "absolute top-1.5 right-1.5 size-1.5"
                            : "ml-auto px-1.5 py-0.5 text-[7px] tracking-[0.08em] text-black uppercase"
                        )}
                      >
                        {labelsCollapsed ? null : "Update"}
                      </span>
                    ) : null}
                  </NavLink>
                );
              })}
            </>
          )}
        </nav>

        <SidebarFooter
          collapsed={labelsCollapsed}
          identity={identity}
          identityError={identityError}
          identityLoading={identityLoading}
          onCollapsedChange={onCollapsedChange}
        />
      </aside>
    </>
  );
};
