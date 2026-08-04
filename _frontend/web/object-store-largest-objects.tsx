import { Search, X } from "lucide-react";
import { useEffect, useState } from "react";

import {
  cancelLargestObjectsSearch,
  fetchLargestObjectsSearch,
  startLargestObjectsSearch,
} from "@/api";
import type { LargestObjectsSearch } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";

const POLL_INTERVAL_MILLIS = 2000;

const compact = (value: number) =>
  Intl.NumberFormat(undefined, {
    maximumFractionDigits: 1,
    notation: "compact",
  }).format(value);

const formatBytes = (value: number) => {
  if (value === 0) {
    return "0 B";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const unit = Math.min(
    Math.max(0, Math.floor(Math.log(value) / Math.log(1024))),
    units.length - 1
  );
  const precision = unit === 0 && Number.isInteger(value) ? 0 : 1;
  return `${(value / 1024 ** unit).toFixed(precision)} ${units[unit]}`;
};

const isActive = (status?: LargestObjectsSearch["status"]) =>
  status === "running" || status === "cancelling";

const isAbortError = (error: unknown) =>
  error instanceof DOMException && error.name === "AbortError";

const errorMessage = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const useLargestObjectsSearch = (projectID: string, storeID: string) => {
  const [search, setSearch] = useState<LargestObjectsSearch>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const active = isActive(search?.status);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setSearch(
          await fetchLargestObjectsSearch(projectID, storeID, controller.signal)
        );
        setError(undefined);
      } catch (loadError) {
        if (isAbortError(loadError)) {
          return;
        }
        setError(
          errorMessage(loadError, "Unable to load largest object search")
        );
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, storeID]);

  useEffect(() => {
    if (!active) {
      return;
    }
    const controller = new AbortController();
    let timer: number | undefined;
    const poll = async () => {
      try {
        const next = await fetchLargestObjectsSearch(
          projectID,
          storeID,
          controller.signal
        );
        setSearch(next);
        setError(undefined);
        if (isActive(next.status)) {
          timer = window.setTimeout(() => void poll(), POLL_INTERVAL_MILLIS);
        }
      } catch (pollError) {
        if (isAbortError(pollError)) {
          return;
        }
        setError(
          errorMessage(pollError, "Unable to refresh largest object search")
        );
        timer = window.setTimeout(() => void poll(), POLL_INTERVAL_MILLIS);
      }
    };
    timer = window.setTimeout(() => void poll(), POLL_INTERVAL_MILLIS);
    return () => {
      controller.abort();
      if (timer !== undefined) {
        window.clearTimeout(timer);
      }
    };
  }, [active, projectID, storeID]);

  const start = async () => {
    setBusy(true);
    try {
      setSearch(await startLargestObjectsSearch(projectID, storeID));
      setError(undefined);
    } catch (startError) {
      setError(
        errorMessage(startError, "Unable to start largest object search")
      );
    } finally {
      setBusy(false);
    }
  };

  const cancel = async () => {
    setBusy(true);
    try {
      setSearch(await cancelLargestObjectsSearch(projectID, storeID));
      setError(undefined);
    } catch (cancelError) {
      setError(
        errorMessage(cancelError, "Unable to cancel largest object search")
      );
    } finally {
      setBusy(false);
    }
  };

  return { active, busy, cancel, error, loading, search, start };
};

const SearchProgress = ({
  estimatedObjects,
  search,
}: {
  estimatedObjects?: number;
  search: LargestObjectsSearch;
}) => {
  const progress =
    estimatedObjects && estimatedObjects > 0
      ? Math.min(100, (100 * search.scannedObjects) / estimatedObjects)
      : undefined;
  const barClassName =
    progress === undefined
      ? "h-full w-1/3 animate-pulse bg-foreground"
      : "h-full bg-foreground transition-[width]";

  return (
    <div aria-live="polite" className="border-b border-border px-5 py-4">
      <div className="flex items-center justify-between text-[9px]">
        <span>
          {search.status === "cancelling"
            ? "Stopping scan…"
            : `${compact(search.scannedObjects)} objects scanned`}
        </span>
        {progress === undefined ? null : (
          <span className="text-muted-foreground">
            {Math.floor(progress)}% of last snapshot
          </span>
        )}
      </div>
      <div className="mt-2 h-1 overflow-hidden bg-muted">
        <div
          className={barClassName}
          style={progress === undefined ? undefined : { width: `${progress}%` }}
        />
      </div>
      <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
        Changes made while this scan is running can affect the result.
      </p>
    </div>
  );
};

const CompletedResults = ({ search }: { search: LargestObjectsSearch }) => {
  if (search.objects.length === 0) {
    return (
      <p className="px-5 py-6 text-center text-[10px] text-muted-foreground">
        The bucket was empty when the scan completed.
      </p>
    );
  }
  return (
    <div>
      <div className="grid grid-cols-[2rem_minmax(0,1fr)_7rem] border-b border-border px-5 py-2 text-[8px] tracking-[0.08em] text-muted-foreground uppercase">
        <span>#</span>
        <span>Object key</span>
        <span className="text-right">Size</span>
      </div>
      <div className="divide-y divide-border">
        {search.objects.map((object, index) => (
          <div
            className="grid grid-cols-[2rem_minmax(0,1fr)_7rem] items-center px-5 py-2.5 text-[10px]"
            key={object.key}
          >
            <span className="text-muted-foreground">{index + 1}</span>
            <span className="truncate pr-4" title={object.key}>
              {object.key}
            </span>
            <span className="text-right">{formatBytes(object.size)}</span>
          </div>
        ))}
      </div>
      <p className="border-t border-border px-5 py-2.5 text-[9px] text-muted-foreground">
        Scanned {compact(search.scannedObjects)} objects.
      </p>
    </div>
  );
};

const SearchBody = ({
  estimatedObjects,
  loading,
  search,
}: {
  estimatedObjects?: number;
  loading: boolean;
  search?: LargestObjectsSearch;
}) => {
  if (!search) {
    return loading ? (
      <p className="px-5 py-6 text-center text-[10px] text-muted-foreground">
        Loading scan status…
      </p>
    ) : null;
  }
  if (isActive(search.status)) {
    return (
      <SearchProgress estimatedObjects={estimatedObjects} search={search} />
    );
  }
  switch (search.status) {
    case "complete": {
      return <CompletedResults search={search} />;
    }
    case "idle": {
      return (
        <p className="px-5 py-6 text-center text-[10px] text-muted-foreground">
          Run a scan to find the ten largest objects in this bucket.
        </p>
      );
    }
    case "cancelled": {
      return (
        <p className="px-5 py-6 text-center text-[10px] text-muted-foreground">
          The scan was cancelled. Partial results were discarded.
        </p>
      );
    }
    case "failed": {
      return (
        <p className="px-5 py-4 text-[10px] text-destructive">
          {search.error ?? "Largest object scan failed"}
        </p>
      );
    }
    default: {
      return null;
    }
  }
};

export const ObjectStoreLargestObjects = ({
  estimatedObjects,
  projectID,
  storeID,
}: {
  estimatedObjects?: number;
  projectID: string;
  storeID: string;
}) => {
  const { active, busy, cancel, error, loading, search, start } =
    useLargestObjectsSearch(projectID, storeID);

  return (
    <SectionCard>
      <header className="flex min-h-14 items-center justify-between gap-4 border-b border-border px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">Largest objects</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            On-demand full metadata scan. Object contents are not read.
          </p>
        </div>
        {active ? (
          <Button
            disabled={busy || search?.status === "cancelling"}
            onClick={() => void cancel()}
            size="sm"
            type="button"
            variant="outline"
          >
            <X />
            {search?.status === "cancelling" ? "Stopping…" : "Cancel scan"}
          </Button>
        ) : (
          <Button
            disabled={busy || loading}
            onClick={() => void start()}
            size="sm"
            type="button"
            variant="outline"
          >
            <Search />
            {busy ? "Starting…" : "Find largest objects"}
          </Button>
        )}
      </header>

      <SearchBody
        estimatedObjects={estimatedObjects}
        loading={loading}
        search={search}
      />

      {error ? (
        <p
          aria-live="polite"
          className="border-t border-border px-5 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </SectionCard>
  );
};
