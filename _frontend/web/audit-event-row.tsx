import { useState } from "react";

import type { AuditEvent } from "@/api";
import {
  formatAuditAction,
  formatAuditActor,
  formatAuditMetadata,
  formatAuditStatus,
  formatAuditTarget,
} from "@/audit-format";
import { DataTableCell, DataTableRow } from "@/components/ui/data-table";
import { cn } from "@/lib/utils";

const collapsedMetadataCount = 3;

const shortID = (value: string) =>
  value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;

interface AuditEventRowProperties {
  columns: string;
  event: AuditEvent;
}

export const AuditEventRow = ({ columns, event }: AuditEventRowProperties) => {
  const [metadataExpanded, setMetadataExpanded] = useState(false);
  const actor = formatAuditActor(event);
  const target = formatAuditTarget(event);
  const metadata = formatAuditMetadata(event.metadata);
  const visibleMetadata = metadataExpanded
    ? metadata
    : metadata.slice(0, collapsedMetadataCount);
  const hiddenMetadataCount = metadata.length - visibleMetadata.length;

  return (
    <DataTableRow columns={columns}>
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
        <span className="inline-flex items-center gap-1.5">
          <span
            aria-hidden="true"
            className={cn(
              "size-1.5 shrink-0",
              event.result === "succeeded" ? "bg-emerald-500" : "bg-rose-500"
            )}
          />
          {formatAuditStatus(event.result)}
        </span>
      </DataTableCell>
      <DataTableCell
        className="truncate font-medium"
        title={formatAuditAction(event.action)}
      >
        {formatAuditAction(event.action)}
      </DataTableCell>
      <DataTableCell
        className="truncate"
        title={`${actor.primary} · ${actor.secondary}`}
      >
        <div className="truncate">{actor.primary}</div>
        <div className="mt-0.5 truncate text-[9px] text-muted-foreground">
          {actor.secondary}
        </div>
      </DataTableCell>
      <DataTableCell title={`${target.primary} · ID ${event.targetId}`}>
        <div className="truncate">{target.primary}</div>
        <div className="mt-0.5 truncate text-[9px] text-muted-foreground">
          ID {target.secondary}
          {event.requestCorrelationId
            ? ` · Request ${shortID(event.requestCorrelationId)}`
            : ""}
        </div>
        {visibleMetadata.length > 0 ? (
          <div className="mt-1 flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-1 text-[9px]">
            {visibleMetadata.map((item) => (
              <span className="max-w-full min-w-0 break-words" key={item.key}>
                <span className="text-muted-foreground">{item.label}:</span>{" "}
                {item.value}
              </span>
            ))}
            {metadata.length > collapsedMetadataCount ? (
              <button
                aria-expanded={metadataExpanded}
                className="text-muted-foreground underline-offset-2 hover:text-foreground hover:underline focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-none"
                onClick={() => setMetadataExpanded((current) => !current)}
                type="button"
              >
                {metadataExpanded
                  ? "Show less"
                  : `Show ${hiddenMetadataCount} more`}
              </button>
            ) : null}
          </div>
        ) : null}
      </DataTableCell>
    </DataTableRow>
  );
};
