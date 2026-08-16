import {
  Bug,
  ChevronRight,
  LoaderCircle,
  RefreshCw,
  Search,
} from "lucide-react";
import { useQueryState } from "nuqs";
import { useDeferredValue, useEffect, useState } from "react";

import { fetchScopedIssues } from "@/api";
import type { MetricScope, ScopedIssue } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { scopedErrorQueryParser } from "@/telemetry-query-state";

const issueDate = (value: string) => {
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString();
};

export const ScopedErrors = ({
  onOpenIssue,
  scope,
  serviceName,
}: {
  onOpenIssue: (issue: ScopedIssue) => void;
  scope: Exclude<MetricScope, { kind: "service" }>;
  serviceName?: (serviceID: string) => string | undefined;
}) => {
  const [query, setQuery] = useQueryState("errorQuery", scopedErrorQueryParser);
  const deferredQuery = useDeferredValue(query);
  const [issues, setIssues] = useState<ScopedIssue[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      setLoading(true);
      try {
        const result = await fetchScopedIssues(
          scope,
          deferredQuery,
          controller.signal
        );
        setIssues(result.data);
        setTotal(result.total);
        setError("");
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load issues"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [deferredQuery, revision, scope]);

  return (
    <div>
      <header className="flex min-h-12 flex-wrap items-center gap-2 border-b border-border px-3 py-2">
        <div className="relative min-w-64 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="Search issues"
            className="h-8 pl-7 text-[10px]"
            maxLength={256}
            onChange={(event) => void setQuery(event.target.value)}
            placeholder="Search issues"
            type="search"
            value={query}
          />
        </div>
        <span className="px-2 text-[9px] text-muted-foreground tabular-nums">
          {total.toLocaleString()}
        </span>
        <Button
          aria-label="Refresh issues"
          onClick={() => setRevision((value) => value + 1)}
          size="icon"
          variant="ghost"
        >
          <RefreshCw className={cn(loading && "animate-spin")} />
        </Button>
      </header>
      {error ? (
        <p className="border-b border-destructive/35 bg-destructive/5 px-4 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      {issues.length > 0 ? (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[760px] table-fixed border-collapse">
            <thead>
              <tr className="h-10 text-left text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
                <th className="w-[42%] border-b border-border px-4 font-normal">
                  Issue
                </th>
                <th className="border-b border-border px-4 font-normal">
                  Service
                </th>
                <th className="border-b border-border px-4 font-normal">
                  Status
                </th>
                <th className="border-b border-border px-4 font-normal">
                  Events
                </th>
                <th className="border-b border-border px-4 font-normal">
                  Last seen
                </th>
                <th className="w-10 border-b border-border">
                  <span className="sr-only">Open</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {issues.map((issue) => (
                <tr
                  className="cursor-pointer transition-colors hover:bg-muted/30"
                  key={`${issue.serviceId}:${issue.id}`}
                  onClick={() => onOpenIssue(issue)}
                >
                  <td className="border-b border-border/70 px-4 py-3">
                    <span className="block truncate text-[10px] font-medium">
                      {issue.title || "Untitled issue"}
                    </span>
                    <code className="mt-1 block truncate text-[8px] text-muted-foreground">
                      {issue.id}
                    </code>
                  </td>
                  <td className="border-b border-border/70 px-4 py-3 text-[9px] text-muted-foreground">
                    <span className="block truncate" title={issue.serviceId}>
                      {serviceName?.(issue.serviceId) ?? issue.serviceId}
                    </span>
                  </td>
                  <td className="border-b border-border/70 px-4 py-3 text-[9px] uppercase">
                    {issue.status}
                  </td>
                  <td className="border-b border-border/70 px-4 py-3 text-[9px] tabular-nums">
                    {issue.eventCount.toLocaleString()}
                  </td>
                  <td className="border-b border-border/70 px-4 py-3 text-[9px] text-muted-foreground">
                    {issueDate(issue.lastSeen)}
                  </td>
                  <td className="border-b border-border/70">
                    <span className="grid size-9 place-items-center text-muted-foreground">
                      <ChevronRight className="size-3.5" />
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="grid min-h-72 place-items-center px-8 text-center">
          <div>
            {loading ? (
              <LoaderCircle className="mx-auto size-5 animate-spin text-muted-foreground" />
            ) : (
              <Bug className="mx-auto size-5 text-muted-foreground" />
            )}
            <p className="mt-4 text-xs font-medium">
              {loading ? "Loading issues" : "No matching issues"}
            </p>
            <p className="mt-2 text-[9px] text-muted-foreground">
              Errors from services in this scope appear here.
            </p>
          </div>
        </div>
      )}
    </div>
  );
};
