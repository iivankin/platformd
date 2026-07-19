import { AlertTriangle, Gauge, HardDrive } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchDiskPressure } from "@/api";
import type { DiskPressure } from "@/api";
import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
import { cn } from "@/lib/utils";

const levelColor: Record<DiskPressure["level"], string> = {
  critical: "bg-orange-500",
  emergency: "bg-rose-500",
  low: "bg-amber-400",
  normal: "bg-emerald-500",
};

const otherComponentPresentation = {
  color: "bg-muted-foreground/30",
  label: "Other disk data",
};

const componentPresentation: Record<string, { color: string; label: string }> =
  {
    backup_work: { color: "bg-violet-500", label: "Backup work files" },
    container_images: { color: "bg-sky-500", label: "Container images" },
    emergency_reserve: { color: "bg-zinc-500", label: "Emergency reserve" },
    logs: { color: "bg-amber-500", label: "Logs" },
    object_storage: { color: "bg-cyan-500", label: "Object storage" },
    other: otherComponentPresentation,
    platform_state: { color: "bg-fuchsia-500", label: "Platform state" },
    postgres_extensions: {
      color: "bg-indigo-500",
      label: "PostgreSQL extension cache",
    },
    registry: { color: "bg-rose-500", label: "Registry" },
    releases: { color: "bg-lime-500", label: "Platform releases" },
    volumes: { color: "bg-emerald-500", label: "Volumes" },
  };

const bytes = (value: number) => {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let current = value;
  let unit = 0;
  while (current >= 1024 && unit < units.length - 1) {
    current /= 1024;
    unit += 1;
  }
  return `${current.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

const Meter = ({
  basisPoints,
  label,
}: {
  basisPoints: number;
  label: string;
}) => (
  <div className="px-5 py-5">
    <div className="flex items-center justify-between text-[10px]">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-medium text-foreground">
        {(basisPoints / 100).toFixed(1)}%
      </span>
    </div>
    <div className="mt-3 h-2 overflow-hidden bg-muted">
      <div
        className="h-full bg-foreground transition-[width] duration-500"
        style={{ width: `${basisPoints / 100}%` }}
      />
    </div>
  </div>
);

export const InfrastructureCapacityPage = () => {
  const [pressure, setPressure] = useState<DiskPressure>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    let inFlight = false;
    const load = async () => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      try {
        setPressure(await fetchDiskPressure(controller.signal));
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to read server capacity"
          );
        }
      } finally {
        inFlight = false;
      }
    };
    void load();
    const interval = window.setInterval(() => void load(), 5000);
    return () => {
      controller.abort();
      window.clearInterval(interval);
    };
  }, []);

  const usedBytes = pressure
    ? pressure.totalBytes - pressure.availableBytes
    : 0;
  const trackedBytes =
    pressure?.components.reduce(
      (total, component) => total + component.bytes,
      0
    ) ?? 0;
  const components = pressure
    ? [
        ...pressure.components,
        ...(usedBytes > trackedBytes
          ? [{ bytes: usedBytes - trackedBytes, id: "other" }]
          : []),
      ]
    : [];
  const breakdownBytes = Math.max(usedBytes, trackedBytes);
  return (
    <PageStack>
      <SectionCard className="flex min-h-24 items-center px-5 py-5">
        <div
          className={cn(
            "mr-4 grid size-10 place-items-center bg-muted",
            pressure ? levelColor[pressure.level] : "bg-muted"
          )}
        >
          <Gauge className="size-4 text-white" />
        </div>
        <div>
          <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
            Storage health
          </p>
          <h3 className="mt-1 text-lg font-medium capitalize">
            {pressure?.level ?? "Checking"}
          </h3>
        </div>
        <p className="ml-auto text-right text-[9px] text-muted-foreground">
          {pressure
            ? `Updated ${new Date(pressure.checkedAt).toLocaleTimeString()}`
            : "Waiting for the first reading"}
        </p>
      </SectionCard>

      {error ? (
        <SectionCard className="flex items-center gap-2 bg-rose-500/5 px-5 py-3 text-xs text-rose-600 ring-rose-500/30 dark:text-rose-300">
          <AlertTriangle className="size-4" />
          {error}
        </SectionCard>
      ) : null}

      <SectionCard className="grid md:grid-cols-[minmax(16rem,1fr)_minmax(16rem,1fr)]">
        <Meter
          basisPoints={pressure?.byteBasisPoints ?? 0}
          label="Disk space used"
        />
        <div className="border-t border-border px-5 py-5 md:border-t-0 md:border-l">
          <HardDrive className="mb-3 size-4 text-muted-foreground" />
          <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
            Available space
          </p>
          <p className="mt-2 text-xs">
            {pressure ? bytes(pressure.availableBytes) : "—"}
          </p>
          <p className="mt-1 text-[9px] text-muted-foreground">
            {pressure
              ? `${bytes(usedBytes)} of ${bytes(pressure.totalBytes)} used`
              : "Reading server storage"}
          </p>
        </div>
      </SectionCard>

      <SectionCard>
        <div className="flex items-start justify-between gap-4 border-b border-border px-5 py-4">
          <div>
            <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
              Disk breakdown
            </p>
            <h3 className="mt-1 text-sm font-medium">Space by component</h3>
          </div>
          <p className="text-right text-[9px] text-muted-foreground">
            {pressure?.componentsCheckedAt
              ? `Scanned ${new Date(
                  pressure.componentsCheckedAt
                ).toLocaleTimeString()}`
              : "Waiting for component scan"}
          </p>
        </div>

        <div className="px-5 py-5">
          <div
            aria-label="Used disk space by component"
            className="flex h-5 w-full overflow-hidden bg-muted"
          >
            {components
              .filter((component) => component.bytes > 0)
              .map((component) => {
                const presentation =
                  componentPresentation[component.id] ??
                  otherComponentPresentation;
                return (
                  <div
                    className={cn(
                      "h-full border-r border-background/50 last:border-r-0",
                      presentation.color
                    )}
                    key={component.id}
                    style={{
                      width: `${breakdownBytes > 0 ? (component.bytes / breakdownBytes) * 100 : 0}%`,
                    }}
                    title={`${presentation.label}: ${bytes(component.bytes)}`}
                  />
                );
              })}
          </div>
          <div className="mt-3 flex items-center justify-between text-[9px] text-muted-foreground">
            <span>{bytes(usedBytes)} used</span>
            <span>{bytes(pressure?.totalBytes ?? 0)} total</span>
          </div>
        </div>

        <div className="grid border-t border-border sm:grid-cols-2 xl:grid-cols-3">
          {components.map((component, index) => {
            const presentation =
              componentPresentation[component.id] ?? otherComponentPresentation;
            return (
              <div
                className={cn(
                  "flex items-center gap-3 border-t border-border px-5 py-3",
                  index === 0 && "border-t-0",
                  index < 2 && "sm:border-t-0",
                  index >= 2 && "sm:border-t",
                  index % 2 === 1 && "sm:border-l",
                  index < 3 && "xl:border-t-0",
                  index >= 3 && "xl:border-t",
                  index % 3 === 0 && "xl:border-l-0",
                  index % 3 !== 0 && "xl:border-l"
                )}
                key={component.id}
              >
                <span className={cn("size-2 shrink-0", presentation.color)} />
                <span className="min-w-0 flex-1 truncate text-[10px]">
                  {presentation.label}
                </span>
                <span className="text-[10px] text-muted-foreground tabular-nums">
                  {bytes(component.bytes)}
                </span>
              </div>
            );
          })}
        </div>
      </SectionCard>
    </PageStack>
  );
};
