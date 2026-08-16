import { Link2 } from "lucide-react";

import type { ServiceTraceSpan } from "@/api";
import {
  traceSemanticContext,
  traceSpanEvents,
  traceSpanLinks,
} from "@/trace-span-context";

const nanosToDate = (value: string) =>
  new Date(Number(BigInt(value) / 1_000_000n));

export const TraceSpanContext = ({ span }: { span: ServiceTraceSpan }) => {
  const semanticContext = traceSemanticContext(span);
  const events = traceSpanEvents(span);
  const links = traceSpanLinks(span);
  return (
    <>
      {semanticContext.map((group) => (
        <div className="border-t border-border" key={group.label}>
          <p className="bg-muted/15 px-4 py-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            {group.label}
          </p>
          <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-y-2 px-4 py-3 text-[9px]">
            {group.values.map((entry) => (
              <div className="contents" key={`${group.label}:${entry.label}`}>
                <dt className="text-muted-foreground">{entry.label}</dt>
                <dd className="min-w-0 font-mono break-words">{entry.value}</dd>
              </div>
            ))}
          </dl>
        </div>
      ))}
      {events.length > 0 ? (
        <div className="border-t border-border">
          <p className="bg-muted/15 px-4 py-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            Span events · {events.length.toLocaleString()}
          </p>
          {events.map((event, index) => (
            <div
              className="border-t border-border/60 px-4 py-3 text-[9px] first:border-t-0"
              key={`${event.name}:${event.timeUnixNano ?? index.toString()}`}
            >
              <div className="flex items-center justify-between gap-3">
                <p className="font-medium">{event.name}</p>
                {event.timeUnixNano ? (
                  <time className="text-[8px] text-muted-foreground">
                    {nanosToDate(event.timeUnixNano).toLocaleString()}
                  </time>
                ) : null}
              </div>
              {event.attributes.map((entry) => (
                <div
                  className="mt-2 grid grid-cols-[7rem_minmax(0,1fr)] gap-2"
                  key={entry.label}
                >
                  <code className="text-muted-foreground">{entry.label}</code>
                  <code className="break-words">{entry.value}</code>
                </div>
              ))}
            </div>
          ))}
        </div>
      ) : null}
      {links.length > 0 ? (
        <div className="border-t border-border">
          <p className="flex items-center gap-1.5 bg-muted/15 px-4 py-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            <Link2 className="size-3" /> Span links ·{" "}
            {links.length.toLocaleString()}
          </p>
          {links.map((link, index) => (
            <div
              className="grid grid-cols-[7rem_minmax(0,1fr)] gap-y-2 border-t border-border/60 px-4 py-3 font-mono text-[9px] first:border-t-0"
              key={`${link.traceID ?? "trace"}:${link.spanID ?? index.toString()}`}
            >
              <span className="text-muted-foreground">Trace</span>
              <span className="break-all">{link.traceID ?? "—"}</span>
              <span className="text-muted-foreground">Span</span>
              <span className="break-all">{link.spanID ?? "—"}</span>
            </div>
          ))}
        </div>
      ) : null}
    </>
  );
};
