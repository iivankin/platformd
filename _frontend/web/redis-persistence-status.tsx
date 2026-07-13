import { LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchManagedRedisPersistence } from "@/api";
import type { ManagedRedisPersistence } from "@/api";

const refreshMillis = 30_000;

const formatAge = (milliseconds: number) => {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) {
    return `${minutes}m ${seconds % 60}s`;
  }
  const hours = Math.floor(minutes / 60);
  return `${hours}h ${minutes % 60}m`;
};

export const RedisPersistenceStatus = ({
  projectID,
  redisID,
}: {
  projectID: string;
  redisID: string;
}) => {
  const [report, setReport] = useState<ManagedRedisPersistence | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setReport(
          await fetchManagedRedisPersistence(
            projectID,
            redisID,
            controller.signal
          )
        );
        setError(null);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to read Redis persistence status"
        );
      }
    };
    void load();
    const interval = globalThis.setInterval(() => void load(), refreshMillis);
    return () => {
      controller.abort();
      globalThis.clearInterval(interval);
    };
  }, [projectID, redisID]);

  if (!report) {
    return (
      <section className="flex items-center gap-2 border-b border-border px-4 py-3 text-[10px] text-muted-foreground">
        {error ? null : <LoaderCircle className="size-3 animate-spin" />}
        <span className={error ? "text-destructive" : undefined}>
          {error ?? "Reading Redis persistence status…"}
        </span>
      </section>
    );
  }

  let state = "Last RDB save failed";
  if (report.lastBackgroundSaveSuccessful) {
    state = "Last RDB save succeeded";
  }
  if (report.backgroundSaveInProgress) {
    state = "RDB save in progress";
  }
  return (
    <section
      className={`grid border-b border-border text-[10px] sm:grid-cols-3 ${
        report.needsAttention ? "bg-destructive/5" : ""
      }`}
    >
      <div className="border-b border-border px-4 py-3 sm:border-r sm:border-b-0">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Actual RPO
        </p>
        <p
          className={`mt-1 ${report.needsAttention ? "text-destructive" : ""}`}
        >
          {formatAge(report.actualRpoMillis)}
        </p>
        <p className="mt-1 text-[9px] text-muted-foreground">
          Target {formatAge(report.targetRpoMillis)}
        </p>
      </div>
      <div className="border-b border-border px-4 py-3 sm:border-r sm:border-b-0">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Last successful RDB
        </p>
        <p className="mt-1">
          {new Date(report.lastSuccessfulSaveAt).toISOString()}
        </p>
        <p className="mt-1 text-[9px] text-muted-foreground">
          {new Date(report.lastSuccessfulSaveAt).toLocaleString()} local
        </p>
      </div>
      <div className="px-4 py-3">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Persistence worker
        </p>
        <p
          className={`mt-1 ${
            report.lastBackgroundSaveSuccessful ? "" : "text-destructive"
          }`}
        >
          {state}
        </p>
        <p className="mt-1 text-[9px] text-muted-foreground">
          Live read every 30 seconds
        </p>
        {error ? <p className="mt-1 text-destructive">{error}</p> : null}
      </div>
    </section>
  );
};
