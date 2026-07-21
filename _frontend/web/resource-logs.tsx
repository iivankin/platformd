import { AlertTriangle, FileClock, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { fetchResourceLogs } from "@/api";
import type { LogWindow, ResourceLogKind } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { useLogTail } from "@/use-log-tail";

const refreshIntervalMilliseconds = 2000;

export const ResourceLogs = ({
  description = "Automatically refreshes every two seconds.",
  deploymentID,
  kind,
  projectID,
  resourceID,
  title = "Resource logs",
}: {
  description?: string;
  deploymentID?: string;
  kind: ResourceLogKind;
  projectID: string;
  resourceID: string;
  title?: string;
}) => {
  const [contains, setContains] = useState("");
  const [appliedContains, setAppliedContains] = useState("");
  const [window, setWindow] = useState<LogWindow>();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const lastRecord = window?.records.at(-1);
  const logTailRef = useLogTail<HTMLElement>(
    lastRecord
      ? `${window?.records.length}:${lastRecord.timestamp}:${lastRecord.attemptId}:${lastRecord.text}`
      : "empty"
  );

  const refresh = useCallback(() => {
    setRefreshVersion((value) => value + 1);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        setWindow(
          await fetchResourceLogs(
            projectID,
            kind,
            resourceID,
            {
              contains: appliedContains || undefined,
              deploymentId: deploymentID,
              limit: 500,
            },
            controller.signal
          )
        );
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
              : "Unable to load resource logs"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
          timer = setTimeout(refresh, refreshIntervalMilliseconds);
        }
      }
    };
    void load();
    return () => {
      controller.abort();
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [
    appliedContains,
    deploymentID,
    kind,
    projectID,
    refresh,
    refreshVersion,
    resourceID,
  ]);

  return (
    <SectionCard ref={logTailRef}>
      <section className="flex flex-wrap items-end gap-3 border-b border-border px-5 py-4">
        <div className="mr-auto">
          <p className="text-[10px] font-medium">{title}</p>
          <p className="mt-1 text-[9px] text-muted-foreground">{description}</p>
        </div>
        <form
          className="flex min-w-72 items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            setLoading(true);
            setAppliedContains(contains.trim());
            refresh();
          }}
        >
          <label
            className="grid flex-1 gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="resource-log-filter"
          >
            Contains
            <span className="relative">
              <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5" />
              <Input
                className="h-8 pl-8 text-[10px]"
                id="resource-log-filter"
                maxLength={256}
                onChange={(event) => setContains(event.target.value)}
                placeholder="Filter recent records"
                value={contains}
              />
            </span>
          </label>
          <Button
            aria-label="Refresh resource logs"
            onClick={refresh}
            size="icon"
            type="button"
            variant="outline"
          >
            <RefreshCw className={cn(loading && "animate-spin")} />
          </Button>
        </form>
      </section>

      <section className="grid grid-cols-3 border-b border-border text-[9px] text-muted-foreground">
        <div className="border-r border-border px-5 py-3">
          Records{" "}
          <span className="text-foreground">{window?.records.length ?? 0}</span>
        </div>
        <div className="border-r border-border px-5 py-3">
          Refresh <span className="text-foreground">2 seconds</span>
        </div>
        <div className="px-5 py-3">
          Window <span className="text-foreground">500 maximum</span>
        </div>
      </section>

      {error ? (
        <section className="flex items-center gap-2 border-b border-destructive/40 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
          <AlertTriangle className="size-3.5" />
          {error}
        </section>
      ) : null}
      {window?.truncated ? (
        <section className="flex items-center gap-2 border-b border-amber-500/30 bg-amber-500/5 px-5 py-2.5 text-[10px] text-amber-700 dark:text-amber-300">
          <AlertTriangle className="size-3.5" />
          Older records exist outside this bounded window.
        </section>
      ) : null}

      {window?.records.length ? (
        <section
          aria-label="Resource log records"
          className="font-mono text-[10px] leading-5"
        >
          {window.records.map((record, index) => (
            <div
              className="grid border-b border-border/70 md:grid-cols-[190px_72px_150px_minmax(0,1fr)]"
              key={record.attemptId + record.timestamp + index.toString()}
            >
              <time className="px-3 py-2 text-muted-foreground md:border-r md:border-border/70">
                {new Date(record.timestamp).toLocaleString()}
              </time>
              <span
                className={cn(
                  "px-3 py-2 font-semibold md:border-r md:border-border/70",
                  record.stream === "stderr"
                    ? "text-rose-500"
                    : "text-cyan-600 dark:text-cyan-400"
                )}
              >
                {record.stream}
              </span>
              <span className="truncate px-3 py-2 text-muted-foreground md:border-r md:border-border/70">
                {record.attemptId}
              </span>
              <pre className="min-w-0 overflow-x-auto px-3 py-2 whitespace-pre-wrap text-foreground">
                {record.text}
                {record.partial ? (
                  <span className="ml-2 text-amber-500">[partial]</span>
                ) : null}
              </pre>
            </div>
          ))}
        </section>
      ) : (
        <section className="grid min-h-72 place-items-center border-b border-border px-8 text-center">
          <div>
            <FileClock className="mx-auto size-6 text-muted-foreground" />
            <p className="mt-4 text-xs font-medium">
              {loading ? "Loading logs" : "No matching records"}
            </p>
            <p className="mt-2 text-[9px] text-muted-foreground">
              Logs remain bounded and plain text.
            </p>
          </div>
        </section>
      )}
    </SectionCard>
  );
};
