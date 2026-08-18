import { NavLink } from "react-router";

import { cn } from "@/lib/utils";

export const ProjectPageTabs = ({ projectID }: { projectID: string }) => (
  <nav aria-label="Project views" className="flex h-12 items-stretch">
    {[
      { end: true, label: "Canvas", path: `/projects/${projectID}` },
      {
        end: false,
        label: "Web analytics",
        path: `/projects/${projectID}/analytics`,
      },
      {
        end: false,
        label: "Telemetry",
        path: `/projects/${projectID}/telemetry`,
      },
    ].map((tab) => (
      <NavLink
        className={({ isActive }) =>
          cn(
            "relative flex items-center px-3 text-[9px] tracking-[0.1em] text-muted-foreground uppercase after:absolute after:right-3 after:bottom-0 after:left-3 after:h-px after:bg-transparent hover:text-foreground",
            isActive && "text-foreground after:bg-foreground"
          )
        }
        end={tab.end}
        key={tab.path}
        to={tab.path}
      >
        {tab.label}
      </NavLink>
    ))}
  </nav>
);
