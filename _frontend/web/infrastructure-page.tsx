import {
  AlertTriangle,
  Database,
  Gauge,
  HardDrive,
  RefreshCw,
  SquareTerminal,
} from "lucide-react";
import { useEffect, useState } from "react";

import { fetchDiskPressure } from "@/api";
import type { DiskPressure } from "@/api";
import { Button } from "@/components/ui/button";
import { InfrastructureLogs } from "@/infrastructure-logs";
import { cn } from "@/lib/utils";
import { ServerTerminalOverlay } from "@/server-terminal-overlay";
import { useSelfUpdate } from "@/use-self-update";

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
  <div className="border-b border-border px-5 py-5 last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0">
    <div className="flex items-center justify-between text-[10px]">
      <span className="tracking-[0.12em] text-muted-foreground uppercase">
        {label}
      </span>
      <span className="font-medium text-foreground">
        {(basisPoints / 100).toFixed(2)}%
      </span>
    </div>
    <div className="relative mt-4 h-2 overflow-hidden bg-muted">
      <div
        className="h-full bg-foreground transition-[width] duration-500"
        style={{ width: `${basisPoints / 100}%` }}
      />
      {[90, 95, 97].map((threshold) => (
        <span
          className="absolute inset-y-0 w-px bg-background/90"
          key={threshold}
          style={{ left: `${threshold}%` }}
        />
      ))}
    </div>
    <div className="mt-2 flex justify-end gap-3 text-[8px] text-muted-foreground">
      <span>90 low</span>
      <span>95 critical</span>
      <span>97 emergency</span>
    </div>
  </div>
);

const PressureHeader = ({ pressure }: { pressure?: DiskPressure }) => (
  <section className="flex min-h-28 items-center border-b border-border px-5 py-5">
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
        Filesystem pressure
      </p>
      <h2 className="mt-1 text-lg font-medium capitalize">
        {pressure?.level ?? "Measuring"}
      </h2>
    </div>
    <div className="ml-auto text-right text-[9px] text-muted-foreground">
      <p>Derived every 5 seconds</p>
      <p className="mt-1">
        {pressure
          ? new Date(pressure.checkedAt).toLocaleTimeString()
          : "Awaiting first sample"}
      </p>
    </div>
  </section>
);

const AllocationGrid = ({ pressure }: { pressure?: DiskPressure }) => {
  const usedBytes = pressure
    ? pressure.totalBytes - pressure.availableBytes
    : 0;
  const usedInodes = pressure
    ? pressure.totalInodes - pressure.availableInodes
    : 0;
  return (
    <section className="grid border-b border-border md:grid-cols-4">
      <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
        <HardDrive className="mb-3 size-4 text-muted-foreground" />
        <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          Disk allocation
        </p>
        <p className="mt-2 text-xs">
          {pressure
            ? `${bytes(usedBytes)} / ${bytes(pressure.totalBytes)}`
            : "—"}
        </p>
      </div>
      <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
        <Database className="mb-3 size-4 text-muted-foreground" />
        <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          Inode allocation
        </p>
        <p className="mt-2 text-xs">
          {pressure
            ? `${usedInodes.toLocaleString()} / ${pressure.totalInodes.toLocaleString()}`
            : "—"}
        </p>
      </div>
      <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
        <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          Emergency reserve
        </p>
        <p className="mt-2 text-xs">
          {pressure?.reservePresent ? "allocated" : "released"}
        </p>
        <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
          max(1 GiB, 2% filesystem)
        </p>
      </div>
      <div className="px-5 py-4">
        <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          Emergency action
        </p>
        <p className="mt-2 text-xs">
          {pressure?.level === "emergency"
            ? "workloads frozen"
            : "workloads running"}
        </p>
        <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
          Control plane and cleanup remain available.
        </p>
      </div>
    </section>
  );
};

export const InfrastructurePage = () => {
  const [pressure, setPressure] = useState<DiskPressure>();
  const [error, setError] = useState<string>();
  const [terminalOpen, setTerminalOpen] = useState(false);
  const {
    error: updateError,
    start,
    targetVersion,
    updating,
  } = useSelfUpdate();

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
              : "Unable to read host capacity"
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

  return (
    <div className="enter-row min-h-full">
      <PressureHeader pressure={pressure} />

      {error ? (
        <section className="flex items-center gap-2 border-b border-rose-500/30 bg-rose-500/5 px-5 py-3 text-xs text-rose-600 dark:text-rose-300">
          <AlertTriangle className="size-4" />
          {error}
        </section>
      ) : null}

      <section className="grid border-b border-border md:grid-cols-2">
        <Meter
          basisPoints={pressure?.byteBasisPoints ?? 0}
          label="Bytes used"
        />
        <Meter
          basisPoints={pressure?.inodeBasisPoints ?? 0}
          label="Inodes used"
        />
      </section>

      <AllocationGrid pressure={pressure} />

      <section className="flex flex-col gap-5 border-b border-border px-5 py-5 md:flex-row md:items-center">
        <div className="grid size-10 shrink-0 place-items-center bg-muted">
          <SquareTerminal className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
            Server access
          </p>
          <p className="mt-1 text-sm font-medium">Interactive root console</p>
          <p className="mt-2 max-w-2xl text-[10px] leading-4 text-muted-foreground">
            Opens an ephemeral host PTY after Cloudflare Access and console
            passphrase verification. Input and output are not recorded.
          </p>
        </div>
        <Button
          className="shrink-0"
          onClick={() => setTerminalOpen(true)}
          type="button"
          variant="outline"
        >
          <SquareTerminal />
          Open console
        </Button>
      </section>

      <section className="flex flex-col gap-5 border-b border-border px-5 py-5 md:flex-row md:items-center">
        <div className="grid size-10 shrink-0 place-items-center bg-muted">
          <RefreshCw className={cn("size-4", updating && "animate-spin")} />
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
            Platform release
          </p>
          <p className="mt-1 text-sm font-medium">
            {targetVersion
              ? `Restarting into ${targetVersion}`
              : "Signed self-update"}
          </p>
          <p className="mt-2 max-w-2xl text-[10px] leading-4 text-muted-foreground">
            Update starts only while deployments, backups, data mutations, SQL,
            and terminals are idle. All workloads stop during the cutover and
            are recreated by the new release.
          </p>
          {updateError ? (
            <p className="mt-2 text-[10px] text-rose-600 dark:text-rose-300">
              {updateError}
            </p>
          ) : null}
        </div>
        <Button
          className="shrink-0"
          disabled={updating}
          onClick={() => void start()}
          type="button"
        >
          {updating ? "Waiting for restart" : "Update platform"}
        </Button>
      </section>

      <InfrastructureLogs />

      {terminalOpen ? (
        <ServerTerminalOverlay onClose={() => setTerminalOpen(false)} />
      ) : null}
    </div>
  );
};
