import { AlertTriangle, FileText, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { fetchInfrastructureLogs } from "@/api";
import type { InfrastructureLogWindow } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const priorities = [
  "emergency",
  "alert",
  "critical",
  "error",
  "warning",
  "notice",
  "info",
  "debug",
] as const;

export const InfrastructureLogs = () => {
  const [journal, setJournal] = useState<InfrastructureLogWindow>();
  const [contains, setContains] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const load = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    try {
      setJournal(await fetchInfrastructureLogs(500, signal));
      setError(undefined);
    } catch (loadError) {
      if (
        !(loadError instanceof DOMException && loadError.name === "AbortError")
      ) {
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to read the platform journal"
        );
      }
    } finally {
      if (!signal?.aborted) {
        setLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const loadInitial = async () => {
      try {
        const window = await fetchInfrastructureLogs(500, controller.signal);
        setJournal(window);
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
              : "Unable to read the platform journal"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void loadInitial();
    return () => controller.abort();
  }, []);

  const records = useMemo(() => {
    const needle = contains.trim().toLocaleLowerCase();
    if (!needle) {
      return journal?.records ?? [];
    }
    return (journal?.records ?? []).filter((record) =>
      `${record.identifier ?? ""} ${record.pid ?? ""} ${record.message}`
        .toLocaleLowerCase()
        .includes(needle)
    );
  }, [contains, journal]);

  return (
    <>
      <section className="flex flex-wrap items-end gap-3 border-b border-border px-5 py-4">
        <div className="mr-auto flex items-center gap-3">
          <div className="grid size-9 place-items-center bg-muted">
            <FileText className="size-4" />
          </div>
          <div>
            <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
              platformd.service
            </p>
            <p className="mt-1 text-sm font-medium">Platform journal</p>
          </div>
        </div>
        <label
          className="grid min-w-64 gap-1.5 text-[10px] text-muted-foreground"
          htmlFor="infrastructure-log-filter"
        >
          Filter loaded records
          <span className="relative">
            <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5" />
            <Input
              className="h-8 pl-8 text-xs"
              id="infrastructure-log-filter"
              onChange={(event) => setContains(event.target.value)}
              placeholder="Message, identifier, or PID"
              value={contains}
            />
          </span>
        </label>
        <Button
          disabled={loading}
          onClick={() => void load()}
          size="sm"
          type="button"
          variant="outline"
        >
          <RefreshCw className={cn(loading && "animate-spin")} />
          Refresh
        </Button>
      </section>

      {error ? (
        <section className="flex items-center gap-2 border-b border-rose-500/30 bg-rose-500/5 px-5 py-3 text-xs text-rose-600 dark:text-rose-300">
          <AlertTriangle className="size-4" />
          {error}
        </section>
      ) : null}
      {journal?.truncated ? (
        <section className="border-b border-amber-500/30 bg-amber-500/5 px-5 py-2.5 text-[10px] text-amber-700 dark:text-amber-300">
          Only the bounded newest journal window is loaded.
        </section>
      ) : null}

      <section
        aria-label="Platform journal records"
        className="font-mono text-[10px] leading-5"
      >
        {records.length === 0 ? (
          <p className="border-b border-border px-5 py-12 text-center text-muted-foreground">
            {loading
              ? "Reading platform journal…"
              : "No matching platform logs"}
          </p>
        ) : (
          records.map((record) => (
            <div
              className="grid border-b border-border/70 md:grid-cols-[190px_86px_130px_minmax(0,1fr)]"
              key={record.cursor}
            >
              <time className="px-3 py-2 text-muted-foreground md:border-r md:border-border/70">
                {new Date(record.timestamp).toLocaleString()}
              </time>
              <span className="px-3 py-2 text-muted-foreground md:border-r md:border-border/70">
                {priorities[record.priority]}
              </span>
              <span className="px-3 py-2 text-muted-foreground md:border-r md:border-border/70">
                {record.identifier ?? "platformd"}
                {record.pid ? `:${record.pid}` : ""}
              </span>
              <pre className="min-w-0 overflow-x-auto px-3 py-2 whitespace-pre-wrap text-foreground">
                {record.message}
              </pre>
            </div>
          ))
        )}
      </section>
    </>
  );
};
