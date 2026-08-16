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
  label: "System & untracked data",
};

const componentPresentation: Record<
  string,
  { color: string; label: string; retention?: string }
> = {
  backup_work: { color: "bg-violet-500", label: "Backup work files" },
  cloudflare_mesh: { color: "bg-blue-500", label: "Cloudflare Mesh state" },
  container_images: {
    color: "bg-sky-500",
    label: "Container images",
    retention: "unused 14d",
  },
  emergency_reserve: { color: "bg-zinc-500", label: "Emergency reserve" },
  image_uploads: {
    color: "bg-orange-500",
    label: "Image uploads",
    retention: "24h",
  },
  images: {
    color: "bg-rose-500",
    label: "Uploaded images",
    retention: "last 7 · previews 14d",
  },
  object_storage: { color: "bg-cyan-500", label: "Object storage" },
  other: otherComponentPresentation,
  platform_state: { color: "bg-fuchsia-500", label: "Platform state" },
  postgres_extensions: {
    color: "bg-indigo-500",
    label: "PostgreSQL extension cache",
  },
  recordings: { color: "bg-teal-300", label: "Recordings", retention: "14d" },
  releases: { color: "bg-lime-500", label: "Platform releases" },
  telemetry: {
    color: "bg-teal-500",
    label: "Telemetry",
    retention: "logs 7d · traces 30d · metrics 30d",
  },
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

type DiskComponent = DiskPressure["components"][number];
const emptyDiskComponents: DiskComponent[] = [];

const nestedComponents = (components: DiskComponent[]) => {
  const nested = new Map<string, DiskComponent[]>();
  for (const component of components) {
    if (!component.parent) {
      continue;
    }
    const children = nested.get(component.parent) ?? [];
    children.push(component);
    nested.set(component.parent, children);
  }
  return nested;
};

const presentationFor = (id: string) =>
  componentPresentation[id] ?? otherComponentPresentation;

const ComponentLabel = ({
  muted,
  presentation,
}: {
  muted?: boolean;
  presentation: { label: string; retention?: string };
}) => (
  <span className="min-w-0 flex-1">
    <span
      className={cn(
        "block truncate text-[10px]",
        muted && "text-muted-foreground"
      )}
    >
      {presentation.label}
    </span>
    {presentation.retention ? (
      <span className="block truncate text-[9px] text-muted-foreground">
        {presentation.retention}
      </span>
    ) : null}
  </span>
);

const ComponentSwatch = ({
  bytes: size,
  id,
  nested,
}: {
  bytes: number;
  id: string;
  nested?: DiskComponent[];
}) => {
  const presentation = presentationFor(id);
  const children = (nested ?? emptyDiskComponents).filter(
    (child) => child.bytes > 0
  );
  const childTotal = children.reduce((total, child) => total + child.bytes, 0);
  const nestedBytes = Math.min(childTotal, size);
  const remainder = size - nestedBytes;
  if (children.length === 0) {
    return (
      <div
        className={cn("h-full", presentation.color)}
        title={`${presentation.label}: ${bytes(size)}`}
      />
    );
  }
  return (
    <div
      className="flex h-full"
      title={`${presentation.label}: ${bytes(size)}`}
    >
      {children.map((child) => {
        const childPresentation = presentationFor(child.id);
        return (
          <div
            className={cn("h-full", childPresentation.color)}
            key={child.id}
            style={{
              width: `${(child.bytes / childTotal) * (nestedBytes / size) * 100}%`,
            }}
            title={`${childPresentation.label}: ${bytes(child.bytes)}`}
          />
        );
      })}
      {remainder > 0 ? (
        <div
          className={cn("h-full", presentation.color)}
          style={{ width: `${(remainder / size) * 100}%` }}
        />
      ) : null}
    </div>
  );
};

const ComponentRow = ({
  bytes: size,
  id,
  index,
  nested,
}: {
  bytes: number;
  id: string;
  index: number;
  nested?: DiskComponent[];
}) => {
  const presentation = presentationFor(id);
  return (
    <div
      className={cn(
        "border-t border-border px-5 py-3",
        index === 0 && "border-t-0",
        index < 2 && "sm:border-t-0",
        index >= 2 && "sm:border-t",
        index % 2 === 1 && "sm:border-l",
        index < 3 && "xl:border-t-0",
        index >= 3 && "xl:border-t",
        index % 3 === 0 && "xl:border-l-0",
        index % 3 !== 0 && "xl:border-l"
      )}
    >
      <div className="flex items-center gap-3">
        <span className={cn("size-2 shrink-0", presentation.color)} />
        <ComponentLabel presentation={presentation} />
        <span className="text-[10px] text-muted-foreground tabular-nums">
          {bytes(size)}
        </span>
      </div>
      {(nested ?? emptyDiskComponents)
        .filter((child) => child.bytes > 0)
        .map((child) => {
          const childPresentation = presentationFor(child.id);
          return (
            <div className="mt-2 flex items-center gap-3 pl-5" key={child.id}>
              <span
                className={cn("size-2 shrink-0", childPresentation.color)}
              />
              <ComponentLabel muted presentation={childPresentation} />
              <span className="text-[10px] text-muted-foreground tabular-nums">
                {bytes(child.bytes)}
              </span>
            </div>
          );
        })}
    </div>
  );
};

const diskBreakdown = (pressure?: DiskPressure) => {
  const usedBytes = pressure?.usedBytes ?? 0;
  const topLevel = (pressure?.components ?? emptyDiskComponents).filter(
    (component) => !component.parent
  );
  const trackedBytes = topLevel.reduce(
    (total, component) => total + component.bytes,
    0
  );
  return {
    breakdownBytes: Math.max(usedBytes, trackedBytes),
    components:
      usedBytes > trackedBytes
        ? [...topLevel, { bytes: usedBytes - trackedBytes, id: "other" }]
        : topLevel,
    nested: nestedComponents(pressure?.components ?? emptyDiskComponents),
    usedBytes,
  };
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

const useDiskPressure = () => {
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

  return { error, pressure };
};

export const InfrastructureCapacityPage = () => {
  const { error, pressure } = useDiskPressure();
  const { breakdownBytes, components, nested, usedBytes } =
    diskBreakdown(pressure);
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
              .map((component) => (
                <div
                  className="h-full border-r border-background/50 last:border-r-0"
                  key={component.id}
                  style={{
                    width: `${breakdownBytes > 0 ? (component.bytes / breakdownBytes) * 100 : 0}%`,
                  }}
                >
                  <ComponentSwatch
                    bytes={component.bytes}
                    id={component.id}
                    nested={nested.get(component.id)}
                  />
                </div>
              ))}
          </div>
          <div className="mt-3 flex items-center justify-between text-[9px] text-muted-foreground">
            <span>{bytes(usedBytes)} used</span>
            <span>{bytes(pressure?.totalBytes ?? 0)} total</span>
          </div>
        </div>

        <div className="grid border-t border-border sm:grid-cols-2 xl:grid-cols-3">
          {components.map((component, index) => (
            <ComponentRow
              bytes={component.bytes}
              id={component.id}
              index={index}
              key={component.id}
              nested={nested.get(component.id)}
            />
          ))}
        </div>
      </SectionCard>
    </PageStack>
  );
};
