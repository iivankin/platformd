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
    </PageStack>
  );
};
