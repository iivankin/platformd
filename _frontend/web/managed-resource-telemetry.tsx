import { useState } from "react";
import type { ReactNode } from "react";

import type { ResourceUsageKind, ResourceUsageRange } from "@/api";
import { cn } from "@/lib/utils";
import { PostgresStats } from "@/postgres-database";
import { RedisStats } from "@/redis-stats";
import { ResourceLogs } from "@/resource-logs";
import { ResourceUsage } from "@/resource-usage";
import { TelemetryWorkspace } from "@/telemetry-workspace";

type ManagedContainerKind = Extract<ResourceUsageKind, "postgres" | "redis">;

const ranges: readonly ResourceUsageRange[] = ["1h", "6h", "1d", "7d", "30d"];

const MetricsRangePicker = ({
  onChange,
  range,
}: {
  onChange: (range: ResourceUsageRange) => void;
  range: ResourceUsageRange;
}) => (
  <div className="flex items-center gap-1 border border-border bg-card px-4 py-2.5">
    <span className="mr-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
      Range
    </span>
    {ranges.map((option) => (
      <button
        className={cn(
          "h-7 border px-2.5 text-[9px] transition-colors",
          range === option
            ? "border-foreground bg-foreground text-background"
            : "border-border text-muted-foreground hover:bg-muted hover:text-foreground"
        )}
        key={option}
        onClick={() => onChange(option)}
        type="button"
      >
        {option}
      </button>
    ))}
  </div>
);

const MetricsLayout = ({
  children,
  description = "Runtime capacity and engine performance in one timeline.",
}: {
  children: ReactNode;
  description?: string;
}) => (
  <div className="space-y-4 p-4 lg:p-6">
    <header className="border-b border-border pb-4">
      <h2 className="text-sm font-medium">Metrics</h2>
      <p className="mt-1 text-[9px] text-muted-foreground">{description}</p>
    </header>
    {children}
  </div>
);

export const ManagedContainerTelemetry = ({
  cpuMillicores,
  kind,
  memoryBytes,
  projectID,
  resourceID,
}: {
  cpuMillicores?: number;
  kind: ManagedContainerKind;
  memoryBytes?: number;
  projectID: string;
  resourceID: string;
}) => {
  const [range, setRange] = useState<ResourceUsageRange>("1h");
  const engineMetrics =
    kind === "postgres" ? (
      <PostgresStats
        postgresID={resourceID}
        projectID={projectID}
        range={range}
        showRange={false}
      />
    ) : (
      <RedisStats
        projectID={projectID}
        range={range}
        redisID={resourceID}
        showRange={false}
      />
    );

  return (
    <TelemetryWorkspace
      views={{
        logs: (
          <ResourceLogs
            kind={kind}
            projectID={projectID}
            resourceID={resourceID}
          />
        ),
        metrics: (
          <MetricsLayout>
            <MetricsRangePicker onChange={setRange} range={range} />
            <ResourceUsage
              cpuMillicores={cpuMillicores}
              kind={kind}
              memoryBytes={memoryBytes}
              onRangeChange={setRange}
              range={range}
              resourceID={resourceID}
              showRange={false}
            />
            {engineMetrics}
          </MetricsLayout>
        ),
      }}
    />
  );
};

export const ObjectStoreTelemetry = ({ metrics }: { metrics: ReactNode }) => (
  <MetricsLayout description="Object storage activity and capacity.">
    {metrics}
  </MetricsLayout>
);
