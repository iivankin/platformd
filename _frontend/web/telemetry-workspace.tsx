import { useQueryState } from "nuqs";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";
import { telemetryViewParser } from "@/telemetry-query-state";
import type { TelemetryView } from "@/telemetry-query-state";

const telemetryViewOrder: readonly TelemetryView[] = [
  "metrics",
  "logs",
  "errors",
  "traces",
  "settings",
];

const telemetryViewLabels: Record<TelemetryView, string> = {
  errors: "Errors",
  logs: "Logs",
  metrics: "Metrics",
  settings: "Settings",
  traces: "Traces",
};

export const TelemetryWorkspace = ({
  views,
}: {
  views: Partial<Record<TelemetryView, ReactNode>>;
}) => {
  const [requestedView, setView] = useQueryState(
    "telemetry",
    telemetryViewParser
  );
  const availableViews = telemetryViewOrder.filter(
    (view) => views[view] !== undefined
  );
  const activeView = Object.hasOwn(views, requestedView)
    ? requestedView
    : (availableViews[0] ?? "metrics");

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden">
      <nav
        aria-label="Telemetry views"
        className="flex h-12 shrink-0 items-stretch overflow-x-auto border-b border-border px-3"
      >
        {availableViews.map((view) => (
          <button
            className={cn(
              "relative shrink-0 px-3 text-[9px] tracking-[0.1em] text-muted-foreground uppercase after:absolute after:right-3 after:bottom-0 after:left-3 after:h-px after:bg-transparent hover:text-foreground",
              view === activeView && "text-foreground after:bg-foreground"
            )}
            key={view}
            onClick={() => void setView(view)}
            type="button"
          >
            {telemetryViewLabels[view]}
          </button>
        ))}
      </nav>
      <div
        className={cn(
          "min-h-0 flex-1",
          activeView === "logs" ? "overflow-hidden" : "overflow-auto"
        )}
      >
        {views[activeView]}
      </div>
    </div>
  );
};
