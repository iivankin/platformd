import { AlertTriangle, Search, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { fetchAuditEvents } from "@/api";
import type { AuditEvent } from "@/api";
import { Button } from "@/components/ui/button";
import { FormCard, SectionCard } from "@/components/ui/card";
import {
  DataTable,
  DataTableBody,
  DataTableCell,
  DataTableHeader,
  DataTableRow,
} from "@/components/ui/data-table";
import { Input } from "@/components/ui/input";
import { PageStack } from "@/components/ui/page-stack";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

const allActors = "__all-actors__";
const anyResult = "__any-result__";
const auditColumns =
  "minmax(170px,190px) minmax(86px,96px) minmax(150px,190px) minmax(170px,210px) minmax(280px,1fr)";

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
    <PageStack className="animate-in duration-200 fade-in slide-in-from-bottom-1">
      <FormCard
        className="flex flex-wrap items-end gap-3 px-5 py-4"
        onSubmit={applyFilters}
      >
        <div className="grid min-w-48 gap-1.5 text-[10px] text-muted-foreground">
          <span>Actor kind</span>
          <Select
            items={{
              [allActors]: "All actors",
              access: "Cloudflare Access",
              local_root: "Local root",
              system: "System",
              token: "API token",
            }}
            onValueChange={(value) =>
              setActorKind(value === allActors ? "" : String(value))
            }
            value={actorKind || allActors}
          >
            <SelectTrigger className="h-8 w-full text-xs" id="audit-actor-kind">
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="start">
              <SelectItem value={allActors}>All actors</SelectItem>
              <SelectItem value="access">Cloudflare Access</SelectItem>
              <SelectItem value="token">API token</SelectItem>
              <SelectItem value="system">System</SelectItem>
              <SelectItem value="local_root">Local root</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div className="grid min-w-40 gap-1.5 text-[10px] text-muted-foreground">
          <span>Result</span>
          <Select
            items={{
              [anyResult]: "Any result",
              failed: "Failed",
              succeeded: "Succeeded",
            }}
            onValueChange={(value) =>
              setResult(value === anyResult ? "" : String(value))
            }
            value={result || anyResult}
          >
            <SelectTrigger className="h-8 w-full text-xs" id="audit-result">
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="start">
              <SelectItem value={anyResult}>Any result</SelectItem>
              <SelectItem value="succeeded">Succeeded</SelectItem>
              <SelectItem value="failed">Failed</SelectItem>
            </SelectContent>
          </Select>
        </div>
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
        <div className="ml-auto flex items-center gap-4 pb-1 font-mono text-[9px] text-muted-foreground">
          <span>
            {events.length} <span className="text-foreground">events</span>
          </span>
          <span>
            retention <span className="text-foreground">7 days</span>
          </span>
        </div>
      </FormCard>

      {error ? (
        <SectionCard className="flex items-center gap-2 bg-destructive/5 px-5 py-3 text-xs text-destructive ring-destructive/40">
          <AlertTriangle className="size-4" />
          {error}
        </SectionCard>
      ) : null}

      <SectionCard className="font-mono">
        <DataTable label="Audit events">
          <DataTableHeader>
            <DataTableRow columns={auditColumns} header>
              <DataTableCell header>Time</DataTableCell>
              <DataTableCell header>Result</DataTableCell>
              <DataTableCell header>Action</DataTableCell>
              <DataTableCell header>Actor</DataTableCell>
              <DataTableCell header>Target / metadata</DataTableCell>
            </DataTableRow>
          </DataTableHeader>
          <DataTableBody>
            {events.map((event) => (
              <DataTableRow columns={auditColumns} key={event.id}>
                <DataTableCell className="text-muted-foreground tabular-nums">
                  <time>{new Date(event.createdAt).toLocaleString()}</time>
                </DataTableCell>
                <DataTableCell
                  className={cn(
                    "font-semibold",
                    event.result === "succeeded"
                      ? "text-emerald-600 dark:text-emerald-400"
                      : "text-rose-500"
                  )}
                >
                  {event.result}
                </DataTableCell>
                <DataTableCell className="truncate" title={event.action}>
                  {event.action}
                </DataTableCell>
                <DataTableCell
                  className="truncate text-muted-foreground"
                  title={event.actorId}
                >
                  {event.actorKind}:{shortID(event.actorId)}
                </DataTableCell>
                <DataTableCell>
                  <div className="truncate">
                    {event.targetKind}:{shortID(event.targetId)}
                  </div>
                  {Object.keys(event.metadata).length ? (
                    <code
                      className="mt-0.5 block truncate text-[9px] text-muted-foreground"
                      title={JSON.stringify(event.metadata)}
                    >
                      {JSON.stringify(event.metadata)}
                    </code>
                  ) : null}
                  {event.requestCorrelationId ? (
                    <div
                      className="mt-0.5 truncate text-[9px] text-muted-foreground"
                      title={event.requestCorrelationId}
                    >
                      request:{shortID(event.requestCorrelationId)}
                    </div>
                  ) : null}
                </DataTableCell>
              </DataTableRow>
            ))}
          </DataTableBody>
        </DataTable>
        {events.length === 0 ? (
          <div className="grid min-h-72 place-items-center px-8 py-16 text-center">
            <div className="max-w-sm">
              <ShieldCheck className="mx-auto mb-5 size-6 text-muted-foreground" />
              <p className="text-xs font-medium">No matching audit events</p>
              <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
                Administrative mutations and security-sensitive sessions appear
                here without command output, secrets, or data values.
              </p>
            </div>
          </div>
        ) : null}
        {nextCursor ? (
          <div className="flex justify-center border-t border-border px-5 py-3">
            <Button
              disabled={loading}
              onClick={() => void readPage(activeFilters, nextCursor)}
              size="sm"
              variant="outline"
            >
              Load older events
            </Button>
          </div>
        ) : null}
      </SectionCard>
    </PageStack>
  );
};
