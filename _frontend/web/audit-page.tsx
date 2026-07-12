import { AlertTriangle, Search, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { fetchAuditEvents } from "@/api";
import type { AuditEvent } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

interface Filters {
  action?: string;
  actorKind?: AuditEvent["actorKind"];
  result?: AuditEvent["result"];
}

const shortID = (value: string) =>
  value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;

export const AuditPage = () => {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [nextCursor, setNextCursor] = useState<string>();
  const [actorKind, setActorKind] = useState("");
  const [action, setAction] = useState("");
  const [result, setResult] = useState("");
  const [activeFilters, setActiveFilters] = useState<Filters>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      setLoading(true);
      try {
        const page = await fetchAuditEvents({ limit: 50 }, controller.signal);
        setEvents(page.events);
        setNextCursor(page.nextCursor);
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
              : "Unable to read audit history"
          );
        }
      } finally {
        setLoading(false);
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const readPage = async (filters: Filters, cursor?: string) => {
    if (loading) {
      return;
    }
    setLoading(true);
    setError(undefined);
    try {
      const page = await fetchAuditEvents({ ...filters, cursor, limit: 50 });
      setEvents((current) =>
        cursor ? [...current, ...page.events] : page.events
      );
      setNextCursor(page.nextCursor);
      setActiveFilters(filters);
    } catch (loadError) {
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to read audit history"
      );
    } finally {
      setLoading(false);
    }
  };

  const applyFilters = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const filters: Filters = {
      ...(action.trim() ? { action: action.trim() } : {}),
      ...(actorKind ? { actorKind: actorKind as AuditEvent["actorKind"] } : {}),
      ...(result ? { result: result as AuditEvent["result"] } : {}),
    };
    void readPage(filters);
  };

  return (
    <div className="enter-row min-h-full">
      <form
        className="flex flex-wrap items-end gap-3 border-b border-border px-5 py-4"
        onSubmit={applyFilters}
      >
        <label
          className="grid min-w-48 gap-1.5 text-[10px] text-muted-foreground"
          htmlFor="audit-actor-kind"
        >
          Actor kind
          <select
            className="h-8 border border-input bg-background px-2.5 text-xs text-foreground outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/30"
            id="audit-actor-kind"
            onChange={(event) => setActorKind(event.target.value)}
            value={actorKind}
          >
            <option value="">All actors</option>
            <option value="access">Cloudflare Access</option>
            <option value="token">API token</option>
            <option value="system">System</option>
            <option value="local_root">Local root</option>
          </select>
        </label>
        <label
          className="grid min-w-40 gap-1.5 text-[10px] text-muted-foreground"
          htmlFor="audit-result"
        >
          Result
          <select
            className="h-8 border border-input bg-background px-2.5 text-xs text-foreground outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/30"
            id="audit-result"
            onChange={(event) => setResult(event.target.value)}
            value={result}
          >
            <option value="">Any result</option>
            <option value="succeeded">Succeeded</option>
            <option value="failed">Failed</option>
          </select>
        </label>
        <label
          className="grid min-w-64 flex-1 gap-1.5 text-[10px] text-muted-foreground"
          htmlFor="audit-action"
        >
          Exact action
          <span className="relative">
            <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5" />
            <Input
              className="h-8 pl-8 font-mono text-[10px]"
              id="audit-action"
              maxLength={128}
              onChange={(event) => setAction(event.target.value)}
              placeholder="service.update"
              value={action}
            />
          </span>
        </label>
        <Button disabled={loading} size="sm" type="submit" variant="outline">
          Apply filters
        </Button>
      </form>

      <section className="grid grid-cols-3 border-b border-border text-[10px] text-muted-foreground">
        <div className="border-r border-border px-5 py-3">
          Visible{" "}
          <span className="ml-1 text-foreground">{events.length} events</span>
        </div>
        <div className="border-r border-border px-5 py-3">
          Retention <span className="ml-1 text-foreground">7 days</span>
        </div>
        <div className="px-5 py-3">
          Ledger{" "}
          <span className="ml-1 text-foreground">local · not tamper-proof</span>
        </div>
      </section>

      {error ? (
        <section className="flex items-center gap-2 border-b border-destructive/40 bg-destructive/5 px-5 py-3 text-xs text-destructive">
          <AlertTriangle className="size-4" />
          {error}
        </section>
      ) : null}

      {events.length ? (
        <section
          aria-label="Audit events"
          className="font-mono text-[10px] leading-5"
        >
          {events.map((event) => (
            <article
              className="grid border-b border-border/70 lg:grid-cols-[185px_90px_170px_180px_minmax(0,1fr)]"
              key={event.id}
            >
              <time className="px-3 py-2.5 text-muted-foreground lg:border-r lg:border-border/70">
                {new Date(event.createdAt).toLocaleString()}
              </time>
              <span
                className={cn(
                  "px-3 py-2.5 font-semibold lg:border-r lg:border-border/70",
                  event.result === "succeeded"
                    ? "text-emerald-600 dark:text-emerald-400"
                    : "text-rose-500"
                )}
              >
                {event.result}
              </span>
              <span className="px-3 py-2.5 text-foreground lg:border-r lg:border-border/70">
                {event.action}
              </span>
              <span
                className="px-3 py-2.5 text-muted-foreground lg:border-r lg:border-border/70"
                title={event.actorId}
              >
                {event.actorKind}:{shortID(event.actorId)}
              </span>
              <div className="min-w-0 px-3 py-2.5">
                <div className="text-foreground">
                  {event.targetKind}:{shortID(event.targetId)}
                </div>
                {Object.keys(event.metadata).length ? (
                  <code className="mt-1 block overflow-x-auto whitespace-pre text-muted-foreground">
                    {JSON.stringify(event.metadata)}
                  </code>
                ) : null}
                {event.requestCorrelationId ? (
                  <div
                    className="mt-1 text-muted-foreground"
                    title={event.requestCorrelationId}
                  >
                    request:{shortID(event.requestCorrelationId)}
                  </div>
                ) : null}
              </div>
            </article>
          ))}
        </section>
      ) : (
        <section className="grid min-h-80 place-items-center border-b border-border px-8 py-16 text-center">
          <div className="max-w-sm">
            <ShieldCheck className="mx-auto mb-5 size-6 text-muted-foreground" />
            <p className="text-xs font-medium">No matching audit events</p>
            <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
              Administrative mutations and security-sensitive sessions appear
              here without command output, secrets, or data values.
            </p>
          </div>
        </section>
      )}

      {nextCursor ? (
        <section className="flex justify-center border-b border-border px-5 py-4">
          <Button
            disabled={loading}
            onClick={() => void readPage(activeFilters, nextCursor)}
            size="sm"
            variant="outline"
          >
            Load older events
          </Button>
        </section>
      ) : null}
    </div>
  );
};
