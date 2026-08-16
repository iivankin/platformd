import { ScrollText } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchResourceLogs, fetchTelemetryLogs } from "@/api";
import type { LogWindow, MetricScope } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { logSeverity } from "@/log-severity";

const severityClass = (severity: string) =>
  cn(
    severity === "error" && "text-destructive",
    severity === "warn" && "text-amber-500",
    severity === "info" && "text-sky-500"
  );

export const TraceRelatedLogs = ({
  onOpenLogs,
  scope,
  traceID,
}: {
  onOpenLogs: (traceID: string) => void;
  scope: MetricScope;
  traceID: string;
}) => {
  const [window, setWindow] = useState<LogWindow>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const next =
          scope.kind === "service"
            ? await fetchResourceLogs(
                scope.projectID,
                "service",
                scope.serviceID,
                { limit: 5, traceId: traceID },
                controller.signal
              )
            : await fetchTelemetryLogs(
                scope,
                { limit: 5, traceId: traceID },
                controller.signal
              );
        setWindow(next);
      } catch (error) {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          setWindow({ records: [], truncated: false });
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [scope, traceID]);

  if (!window || window.records.length === 0) {
    return null;
  }
  return (
    <section className="border-b border-border">
      <header className="flex items-center justify-between gap-3 px-5 py-3 lg:px-7">
        <div className="flex items-center gap-2">
          <ScrollText className="size-3.5 text-muted-foreground" />
          <div>
            <p className="text-[9px] font-medium">Related logs</p>
            <p className="mt-0.5 text-[8px] text-muted-foreground">
              Correlated by trace ID
            </p>
          </div>
        </div>
        <Button onClick={() => onOpenLogs(traceID)} size="sm" variant="ghost">
          View all logs
        </Button>
      </header>
      <div className="border-t border-border">
        {window.records.map((record, index) => {
          const severity = logSeverity(record);
          return (
            <div
              className="grid grid-cols-[7rem_4rem_minmax(0,1fr)] border-b border-border/60 px-5 py-2 font-mono text-[8px] last:border-b-0 lg:px-7"
              key={`${record.timestamp}:${record.spanId ?? ""}:${index.toString()}`}
            >
              <time className="text-muted-foreground">
                {new Date(record.timestamp).toLocaleTimeString()}
              </time>
              <span className={severityClass(severity)}>{severity}</span>
              <span className="truncate">{record.text}</span>
            </div>
          );
        })}
      </div>
    </section>
  );
};
