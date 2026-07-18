import { AlertTriangle, FileText, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { fetchInfrastructureLogs } from "@/api";
import type { InfrastructureLogWindow } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import {
  DataTable,
  DataTableBody,
  DataTableCell,
  DataTableHeader,
  DataTableRow,
} from "@/components/ui/data-table";
import { Input } from "@/components/ui/input";
import { PageStack } from "@/components/ui/page-stack";
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
const logColumns =
  "minmax(180px,200px) minmax(84px,100px) minmax(130px,170px) minmax(360px,1fr)";

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
    <PageStack>
      <SectionCard className="flex flex-wrap items-end gap-3 px-5 py-4">
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
      </SectionCard>

      {error ? (
        <SectionCard className="flex items-center gap-2 bg-rose-500/5 px-5 py-3 text-xs text-rose-600 ring-rose-500/30 dark:text-rose-300">
          <AlertTriangle className="size-4" />
          {error}
        </SectionCard>
      ) : null}
      {journal?.truncated ? (
        <SectionCard className="bg-amber-500/5 px-5 py-2.5 text-[10px] text-amber-700 ring-amber-500/30 dark:text-amber-300">
          Only the bounded newest journal window is loaded.
        </SectionCard>
      ) : null}

      <SectionCard aria-label="Platform journal records" className="font-mono">
        <DataTable label="Platform journal records">
          <DataTableHeader>
            <DataTableRow columns={logColumns} header>
              <DataTableCell header>Time</DataTableCell>
              <DataTableCell header>Priority</DataTableCell>
              <DataTableCell header>Source</DataTableCell>
              <DataTableCell header>Message</DataTableCell>
            </DataTableRow>
          </DataTableHeader>
          <DataTableBody>
            {records.map((record) => (
              <DataTableRow columns={logColumns} key={record.cursor}>
                <DataTableCell className="text-muted-foreground tabular-nums">
                  <time>{new Date(record.timestamp).toLocaleString()}</time>
                </DataTableCell>
                <DataTableCell className="text-muted-foreground">
                  {priorities[record.priority]}
                </DataTableCell>
                <DataTableCell
                  className="truncate text-muted-foreground"
                  title={`${record.identifier ?? "platformd"}${record.pid ? `:${record.pid}` : ""}`}
                >
                  {record.identifier ?? "platformd"}
                  {record.pid ? `:${record.pid}` : ""}
                </DataTableCell>
                <DataTableCell className="leading-5 break-words whitespace-pre-wrap">
                  {record.message}
                </DataTableCell>
              </DataTableRow>
            ))}
          </DataTableBody>
        </DataTable>
        {records.length === 0 ? (
          <p className="px-5 py-12 text-center text-[10px] text-muted-foreground">
            {loading
              ? "Reading platform journal…"
              : "No matching platform logs"}
          </p>
        ) : null}
      </SectionCard>
    </PageStack>
  );
};
