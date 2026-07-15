import {
  AlertTriangle,
  ArchiveRestore,
  Check,
  CircleDashed,
  LoaderCircle,
  RefreshCw,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";

import {
  fetchBackupGenerations,
  fetchRecoveryStatus,
  retryRecovery,
} from "@/api";
import type {
  BackupGeneration,
  RecoveryResource,
  RecoveryResourceKind,
  RecoveryStatus,
} from "@/api";
import { BackupStoragePage } from "@/backup-storage-page";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useResourceRestore } from "@/use-resource-restore";

const recoveryPollMilliseconds = 2000;

const resourcePresentation: Record<RecoveryResourceKind, { label: string }> = {
  object_store: { label: "Object Store" },
  postgres: { label: "PostgreSQL" },
  redis: { label: "Redis" },
  registry: { label: "Registry" },
};

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const bytes = (value: number) => {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

const timestamp = (value?: number) =>
  value
    ? new Date(value).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "medium",
      })
    : "No complete generation · empty resource";

interface RecoveryResourceRowProperties {
  onChanged: () => Promise<void>;
  resource: RecoveryResource;
}

const RecoveryResourceRow = ({
  onChanged,
  resource,
}: RecoveryResourceRowProperties) => {
  const presentation = resourcePresentation[resource.resourceKind];
  const generationController = useRef<AbortController | null>(null);
  const [expanded, setExpanded] = useState(false);
  const [generations, setGenerations] = useState<BackupGeneration[]>([]);
  const [generationsLoading, setGenerationsLoading] = useState(false);
  const [selected, setSelected] = useState<BackupGeneration>();
  const [error, setError] = useState<string>();

  const afterRestore = useCallback(async () => {
    setSelected(undefined);
    await retryRecovery();
    await onChanged();
  }, [onChanged]);
  const restore = useResourceRestore({
    onSucceeded: afterRestore,
    resourceID: resource.resourceId,
    resourceKind: resource.resourceKind,
  });

  useEffect(() => () => generationController.current?.abort(), []);

  const toggleGenerations = async () => {
    if (expanded) {
      setExpanded(false);
      return;
    }
    setExpanded(true);
    if (generations.length > 0 || generationsLoading) {
      return;
    }
    generationController.current?.abort();
    const controller = new AbortController();
    generationController.current = controller;
    setGenerationsLoading(true);
    setError(undefined);
    try {
      setGenerations(
        await fetchBackupGenerations(
          resource.resourceKind,
          resource.resourceId,
          controller.signal
        )
      );
    } catch (loadError) {
      if (
        !(loadError instanceof DOMException && loadError.name === "AbortError")
      ) {
        setError(errorText(loadError, "Unable to list backup generations"));
      }
    } finally {
      if (!controller.signal.aborted) {
        setGenerationsLoading(false);
      }
    }
  };

  const completed = resource.status !== "pending";
  let generationContent: ReactNode;
  if (generationsLoading) {
    generationContent = (
      <div className="flex items-center gap-2 px-5 py-4 text-[10px] text-muted-foreground">
        <LoaderCircle className="size-3.5 animate-spin" />
        Reading the remote catalog
      </div>
    );
  } else if (generations.length === 0) {
    generationContent = (
      <p className="px-5 py-4 text-[10px] text-muted-foreground">
        No complete generations are available for this resource.
      </p>
    );
  } else {
    generationContent = generations.map((generation) => (
      <div
        className="grid items-center gap-3 border-b border-border px-5 py-3 last:border-b-0 md:grid-cols-[minmax(12rem,1fr)_minmax(12rem,1fr)_minmax(8rem,0.6fr)_auto]"
        key={generation.generationId}
      >
        <span className="font-mono text-[9px]">{generation.generationId}</span>
        <span className="text-[9px] text-muted-foreground">
          {timestamp(generation.completedAt)}
        </span>
        <span className="text-[9px] text-muted-foreground">
          {bytes(generation.plaintextSize)} raw · {bytes(generation.remoteSize)}{" "}
          remote
        </span>
        <Button
          disabled={restore.restoring}
          onClick={() => setSelected(generation)}
          size="sm"
          variant="ghost"
        >
          Select
        </Button>
      </div>
    ));
  }

  return (
    <div className="border-b border-border last:border-b-0">
      <div className="grid items-center gap-3 px-5 py-4 md:grid-cols-[minmax(12rem,1fr)_minmax(12rem,1fr)_minmax(13rem,1fr)_auto]">
        <div className="flex min-w-0 items-center gap-3">
          <span
            className={cn(
              "grid size-8 shrink-0 place-items-center border",
              completed
                ? "border-emerald-500/30 bg-emerald-500/5 text-emerald-600"
                : "border-amber-500/30 bg-amber-500/5 text-amber-600"
            )}
          >
            {completed ? (
              <Check className="size-3.5" />
            ) : (
              <LoaderCircle className="size-3.5 animate-spin" />
            )}
          </span>
          <div className="min-w-0">
            <p className="text-[10px] font-medium">{presentation.label}</p>
            <p className="mt-1 truncate font-mono text-[9px] text-muted-foreground">
              {resource.resourceId}
            </p>
          </div>
        </div>
        <div>
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Result
          </p>
          <p className="mt-1.5 text-[10px] capitalize">
            {resource.status === "empty" ? "Created empty" : resource.status}
          </p>
        </div>
        <div className="min-w-0">
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Source snapshot
          </p>
          <p
            className="mt-1.5 truncate text-[10px]"
            title={timestamp(resource.sourceCompletedAt)}
          >
            {timestamp(resource.sourceCompletedAt)}
          </p>
        </div>
        <Button
          disabled={completed}
          onClick={() => void toggleGenerations()}
          size="sm"
          variant="outline"
        >
          <ArchiveRestore />
          {expanded ? "Close generations" : "Choose generation"}
        </Button>
      </div>

      {expanded ? (
        <div className="border-t border-border bg-muted/15">
          <div className="flex items-center gap-2 border-b border-border px-5 py-3 text-[9px] text-muted-foreground">
            <ArchiveRestore className="size-3.5" />
            Complete remote generations, newest first. Selecting one replaces
            this resource only.
          </div>
          {generationContent}
          {selected ? (
            <div className="flex flex-col gap-3 border-t border-amber-500/30 bg-amber-500/5 px-5 py-4 md:flex-row md:items-center">
              <AlertTriangle className="size-4 shrink-0 text-amber-600" />
              <p className="min-w-0 flex-1 text-[10px] leading-4">
                Replace {presentation.label}{" "}
                <span className="font-mono">{resource.resourceId}</span> with
                generation{" "}
                <span className="font-mono">{selected.generationId}</span>?
              </p>
              <Button
                disabled={restore.restoring}
                onClick={() => setSelected(undefined)}
                size="sm"
                variant="ghost"
              >
                Cancel
              </Button>
              <Button
                disabled={restore.restoring}
                onClick={() => {
                  if (selected) {
                    void restore.start(selected.generationId);
                  }
                }}
                size="sm"
              >
                {restore.restoring ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <ArchiveRestore />
                )}
                Replace resource
              </Button>
            </div>
          ) : null}
          {restore.restoring ? (
            <p className="border-t border-border px-5 py-3 text-[9px] text-muted-foreground">
              Restore operation · {restore.operation?.progress || "starting"}
            </p>
          ) : null}
          {error || restore.error ? (
            <p className="border-t border-rose-500/30 bg-rose-500/5 px-5 py-3 text-[10px] text-rose-600 dark:text-rose-300">
              {error || restore.error}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
};

export const RecoveryPage = () => {
  const [status, setStatus] = useState<RecoveryStatus>({ resources: [] });
  const [error, setError] = useState<string>();
  const [retrying, setRetrying] = useState(false);
  const inFlight = useRef(false);

  const load = useCallback(async (signal?: AbortSignal) => {
    if (inFlight.current) {
      return;
    }
    inFlight.current = true;
    try {
      setStatus(await fetchRecoveryStatus(signal));
      setError(undefined);
    } catch (loadError) {
      if (
        !(loadError instanceof DOMException && loadError.name === "AbortError")
      ) {
        setError(errorText(loadError, "Unable to load recovery status"));
      }
    } finally {
      inFlight.current = false;
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const initial = window.setTimeout(() => void load(controller.signal), 0);
    const interval = window.setInterval(
      () => void load(controller.signal),
      recoveryPollMilliseconds
    );
    return () => {
      controller.abort();
      window.clearTimeout(initial);
      window.clearInterval(interval);
    };
  }, [load]);

  const retry = async () => {
    if (retrying) {
      return;
    }
    setRetrying(true);
    setError(undefined);
    try {
      await retryRecovery();
      await load();
    } catch (retryError) {
      setError(errorText(retryError, "Unable to retry recovery"));
    } finally {
      setRetrying(false);
    }
  };

  const complete = status.resources.filter(
    (resource) => resource.status !== "pending"
  ).length;
  const total = status.resources.length;
  const percent = total === 0 ? 0 : Math.round((complete / total) * 100);

  return (
    <div className="enter-row min-h-full">
      <section className="border-b border-border bg-[linear-gradient(135deg,color-mix(in_oklab,var(--muted)_45%,transparent),transparent_62%)] px-5 py-7">
        <div className="flex flex-col gap-5 md:flex-row md:items-end">
          <div className="flex min-w-0 flex-1 gap-4">
            <span className="grid size-11 shrink-0 place-items-center border border-amber-500/35 bg-amber-500/8 text-amber-600">
              <CircleDashed className="size-5 animate-spin [animation-duration:3s]" />
            </span>
            <div>
              <p className="text-[9px] tracking-[0.16em] text-amber-600 uppercase">
                Disaster recovery mode
              </p>
              <h2 className="mt-1 text-lg font-medium">
                Restoring independent resource snapshots
              </h2>
              <p className="mt-2 max-w-3xl text-[10px] leading-4 text-muted-foreground">
                Public application routes and ordinary reconciliation stay off
                until every managed resource is restored or created empty. The
                daemon restarts automatically after the final resource.
              </p>
            </div>
          </div>
          <Button
            disabled={retrying}
            onClick={() => void retry()}
            variant="outline"
          >
            <RefreshCw className={cn(retrying && "animate-spin")} />
            Retry now
          </Button>
        </div>
        <div className="mt-6 flex items-center gap-3">
          <div className="h-1.5 min-w-0 flex-1 overflow-hidden bg-muted">
            <div
              className="h-full bg-amber-500 transition-[width] duration-500"
              style={{ width: `${percent}%` }}
            />
          </div>
          <span className="shrink-0 font-mono text-[9px] text-muted-foreground">
            {complete}/{total} · {percent}%
          </span>
        </div>
      </section>

      {status.lastError ? (
        <section className="flex items-start gap-3 border-b border-rose-500/30 bg-rose-500/5 px-5 py-4">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-rose-600" />
          <div>
            <p className="text-[9px] font-medium text-rose-600 dark:text-rose-300">
              Latest automatic attempt failed
            </p>
            <p className="mt-1 font-mono text-[9px] leading-4 text-muted-foreground">
              {status.lastError}
            </p>
          </div>
        </section>
      ) : null}

      {error ? (
        <section className="border-b border-rose-500/30 bg-rose-500/5 px-5 py-3 text-[10px] text-rose-600 dark:text-rose-300">
          {error}
        </section>
      ) : null}

      <section className="border-b border-border">
        <div className="flex items-center gap-3 border-b border-border px-5 py-3">
          <ArchiveRestore className="size-3.5 text-muted-foreground" />
          <h3 className="text-[10px] font-medium">Recovery resources</h3>
          <span className="ml-auto text-[9px] text-muted-foreground">
            Source times are independent
          </span>
        </div>
        {status.resources.length === 0 ? (
          <div className="flex items-center gap-2 px-5 py-5 text-[10px] text-muted-foreground">
            <LoaderCircle className="size-3.5 animate-spin" />
            Reading restored control state
          </div>
        ) : (
          status.resources.map((resource) => (
            <RecoveryResourceRow
              key={`${resource.resourceKind}:${resource.resourceId}`}
              onChanged={load}
              resource={resource}
            />
          ))
        )}
      </section>

      <BackupStoragePage />
    </div>
  );
};
