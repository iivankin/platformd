import { AlertTriangle, LoaderCircle, ShieldCheck } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { FormEvent } from "react";

import { fetchAuditEvents } from "@/api";
import type { AuditEvent } from "@/api";
import { AuditEventRow } from "@/audit-event-row";
import { auditActionItems, auditActionOptions } from "@/audit-format";
import { Button } from "@/components/ui/button";
import { FormCard, SectionCard } from "@/components/ui/card";
import {
  DataTable,
  DataTableBody,
  DataTableCell,
  DataTableHeader,
  DataTableRow,
} from "@/components/ui/data-table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

const allActors = "__all-actors__";
const allActions = "__all-actions__";
const anyResult = "__any-result__";
const pageAuditColumns =
  "minmax(170px,190px) minmax(86px,96px) minmax(150px,190px) minmax(170px,210px) minmax(280px,1fr)";
const embeddedAuditColumns =
  "minmax(145px,165px) minmax(82px,90px) minmax(145px,170px) minmax(155px,180px) minmax(240px,1fr)";

interface Filters {
  action?: string;
  actorKind?: AuditEvent["actorKind"];
  result?: AuditEvent["result"];
}

interface AuditEventsViewProperties {
  embedded?: boolean;
  projectId?: string;
  projectName?: string;
}

export const AuditEventsView = ({
  embedded = false,
  projectId,
  projectName,
}: AuditEventsViewProperties) => {
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [nextCursor, setNextCursor] = useState<string>();
  const [actorKind, setActorKind] = useState("");
  const [action, setAction] = useState("");
  const [result, setResult] = useState("");
  const [activeFilters, setActiveFilters] = useState<Filters>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string>();
  const requestGeneration = useRef(0);
  const fieldPrefix = projectId ? `project-${projectId}-audit` : "audit";

  useEffect(() => {
    const controller = new AbortController();
    const generation = requestGeneration.current + 1;
    requestGeneration.current = generation;
    const load = async () => {
      setLoading(true);
      setEvents([]);
      setNextCursor(undefined);
      setActiveFilters({});
      try {
        const page = await fetchAuditEvents(
          { limit: 50, ...(projectId ? { projectId } : {}) },
          controller.signal
        );
        if (requestGeneration.current !== generation) {
          return;
        }
        setEvents(page.events);
        setNextCursor(page.nextCursor);
        setError(undefined);
      } catch (loadError) {
        if (requestGeneration.current !== generation) {
          return;
        }
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
        if (requestGeneration.current === generation) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => {
      controller.abort();
      if (requestGeneration.current === generation) {
        requestGeneration.current += 1;
      }
    };
  }, [projectId]);

  const readPage = async (filters: Filters, cursor?: string) => {
    if (loading) {
      return;
    }
    const generation = requestGeneration.current + 1;
    requestGeneration.current = generation;
    setLoading(true);
    setError(undefined);
    try {
      const page = await fetchAuditEvents({
        ...filters,
        ...(projectId ? { projectId } : {}),
        cursor,
        limit: 50,
      });
      if (requestGeneration.current !== generation) {
        return;
      }
      setEvents((current) =>
        cursor ? [...current, ...page.events] : page.events
      );
      setNextCursor(page.nextCursor);
      setActiveFilters(filters);
    } catch (loadError) {
      if (requestGeneration.current !== generation) {
        return;
      }
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to read audit history"
      );
    } finally {
      if (requestGeneration.current === generation) {
        setLoading(false);
      }
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

  const filterControls = (
    <>
      <div className="grid min-w-40 gap-1.5 text-[10px] text-muted-foreground">
        <span>Initiated by</span>
        <Select
          items={{
            [allActors]: "All actors",
            access: "Cloudflare Access",
            local_root: "Local administrator",
            system: "System",
            token: "API token",
          }}
          onValueChange={(value) =>
            setActorKind(value === allActors ? "" : String(value))
          }
          value={actorKind || allActors}
        >
          <SelectTrigger
            className="h-8 w-full text-xs"
            id={`${fieldPrefix}-actor-kind`}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            <SelectItem value={allActors}>All actors</SelectItem>
            <SelectItem value="access">Cloudflare Access</SelectItem>
            <SelectItem value="token">API token</SelectItem>
            <SelectItem value="system">System</SelectItem>
            <SelectItem value="local_root">Local administrator</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="grid min-w-36 gap-1.5 text-[10px] text-muted-foreground">
        <span>Status</span>
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
          <SelectTrigger
            className="h-8 w-full text-xs"
            id={`${fieldPrefix}-result`}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            <SelectItem value={anyResult}>Any result</SelectItem>
            <SelectItem value="succeeded">Succeeded</SelectItem>
            <SelectItem value="failed">Failed</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="grid min-w-52 flex-1 gap-1.5 text-[10px] text-muted-foreground">
        <span>Action</span>
        <Select
          items={auditActionItems}
          onValueChange={(value) =>
            setAction(value === allActions ? "" : String(value))
          }
          value={action || allActions}
        >
          <SelectTrigger
            className="h-8 w-full text-xs"
            id={`${fieldPrefix}-action`}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start" className="max-h-80 min-w-64">
            <SelectItem value={allActions}>All actions</SelectItem>
            {auditActionOptions.map(([value, label]) => (
              <SelectItem key={value} value={value}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
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
    </>
  );

  const errorMessage = error ? (
    <div className="flex items-center gap-2 px-5 py-3 text-xs text-destructive">
      <AlertTriangle className="size-4" />
      {error}
    </div>
  ) : null;

  const table = (
    <>
      <DataTable label={projectId ? "Project audit events" : "Audit events"}>
        <DataTableHeader>
          <DataTableRow
            columns={embedded ? embeddedAuditColumns : pageAuditColumns}
            header
          >
            <DataTableCell header>When</DataTableCell>
            <DataTableCell header>Status</DataTableCell>
            <DataTableCell header>Event</DataTableCell>
            <DataTableCell header>Initiated by</DataTableCell>
            <DataTableCell header>Context</DataTableCell>
          </DataTableRow>
        </DataTableHeader>
        <DataTableBody>
          {events.map((event) => (
            <AuditEventRow
              columns={embedded ? embeddedAuditColumns : pageAuditColumns}
              event={event}
              key={event.id}
            />
          ))}
        </DataTableBody>
      </DataTable>
      {events.length === 0 ? (
        <div className="grid min-h-56 place-items-center px-8 py-14 text-center">
          <div className="max-w-sm">
            {loading ? (
              <LoaderCircle className="mx-auto mb-5 size-6 animate-spin text-muted-foreground" />
            ) : (
              <ShieldCheck className="mx-auto mb-5 size-6 text-muted-foreground" />
            )}
            <p className="text-xs font-medium">
              {loading ? "Loading audit events" : "No matching audit events"}
            </p>
            <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
              Administrative changes and security-sensitive sessions appear here
              without command output, secrets, or data values.
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
    </>
  );

  if (embedded) {
    return (
      <div>
        <header className="border-b border-border px-6 py-5">
          <h3 className="text-sm font-medium">Audit</h3>
          <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
            Activity recorded for {projectName ?? "this project"}.
          </p>
        </header>
        <form
          className="flex flex-wrap items-end gap-3 border-b border-border px-6 py-4"
          onSubmit={applyFilters}
        >
          {filterControls}
        </form>
        {error ? (
          <div className="border-b border-destructive/30 bg-destructive/5">
            {errorMessage}
          </div>
        ) : null}
        <section className="font-mono">{table}</section>
      </div>
    );
  }

  return (
    <>
      <FormCard
        className="flex flex-wrap items-end gap-3 px-5 py-4"
        onSubmit={applyFilters}
      >
        {filterControls}
      </FormCard>
      {error ? (
        <SectionCard className="bg-destructive/5 ring-destructive/40">
          {errorMessage}
        </SectionCard>
      ) : null}
      <SectionCard className="font-mono">{table}</SectionCard>
    </>
  );
};
