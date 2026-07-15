import { NavLink } from "react-router";

import { cn } from "@/lib/utils";

export interface PageTab {
  end?: boolean;
  label: string;
  path: string;
}

export const PageTabs = ({
  label,
  tabs,
}: {
  label: string;
  tabs: PageTab[];
}) => (
  <nav
    aria-label={label}
    className="flex min-h-10 overflow-x-auto border-b border-border px-3"
  >
    {tabs.map((tab) => (
      <NavLink
        className={({ isActive }) =>
          cn(
            "flex shrink-0 items-center border-b-2 border-transparent px-3 text-[10px] text-muted-foreground transition-colors hover:text-foreground",
            isActive && "border-foreground font-medium text-foreground"
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
