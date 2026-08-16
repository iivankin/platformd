import type { ColumnDef } from "@tanstack/react-table";
import { ArrowDown, ArrowUp, ArrowUpDown, Braces, Plus } from "lucide-react";

import { AnsiText } from "@/ansi-text";
import type { LogRecord } from "@/api";
import { cn } from "@/lib/utils";
import { structuredLogFields } from "@/log-field-filter";
import type { LogFieldFilter } from "@/log-field-filter";
import { logSeverity, logSeverityClass } from "@/log-severity";

const shortID = (value: string) =>
  value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;

const SortHeader = ({
  label,
  sorted,
}: {
  label: string;
  sorted: false | "asc" | "desc";
}) => {
  let icon = <ArrowUpDown className="size-3 opacity-45" />;
  if (sorted === "asc") {
    icon = <ArrowUp className="size-3" />;
  } else if (sorted === "desc") {
    icon = <ArrowDown className="size-3" />;
  }
  return (
    <span className="flex items-center gap-1.5">
      {label}
      {icon}
    </span>
  );
};

const StructuredFields = ({
  fields,
  onAddFilter,
}: {
  fields?: Record<string, unknown>;
  onAddFilter: (filter: LogFieldFilter) => void;
}) => {
  const entries = fields ? structuredLogFields(fields) : [];
  if (entries.length === 0) {
    return null;
  }
  return (
    <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1">
      {entries.map(({ path, value }) => (
        <button
          className="group flex min-w-0 items-center gap-1 text-[8px] text-muted-foreground hover:text-foreground"
          key={path}
          onClick={() => onAddFilter({ operator: "equals", path, value })}
          title={`Filter by ${path}`}
          type="button"
        >
          <span className="text-foreground/65">{path}</span>
          <span>=</span>
          <span className="max-w-56 truncate">{value}</span>
          <Plus className="size-2.5 opacity-0 transition-opacity group-hover:opacity-100" />
        </button>
      ))}
    </div>
  );
};

export const logTableColumns = (
  onAddFilter: (filter: LogFieldFilter) => void,
  options: {
    serviceName?: (serviceID: string) => string | undefined;
    showService?: boolean;
  } = {}
): ColumnDef<LogRecord>[] => {
  const columns: ColumnDef<LogRecord>[] = [
    {
      accessorFn: (record) => new Date(record.timestamp).getTime(),
      cell: ({ row }) => (
        <time className="whitespace-nowrap text-muted-foreground tabular-nums">
          {new Date(row.original.timestamp).toLocaleString()}
        </time>
      ),
      header: ({ column }) => (
        <SortHeader label="Timestamp" sorted={column.getIsSorted()} />
      ),
      id: "timestamp",
      size: 176,
    },
    {
      accessorFn: (record) => logSeverity(record),
      cell: ({ row }) => {
        const level = logSeverity(row.original);
        return (
          <span className={cn("uppercase", logSeverityClass[level])}>
            {row.original.severityText || level}
          </span>
        );
      },
      header: ({ column }) => (
        <SortHeader label="Level" sorted={column.getIsSorted()} />
      ),
      id: "severity",
      size: 92,
    },
    ...(options.showService
      ? [
          {
            accessorKey: "serviceId",
            cell: ({ getValue }) => {
              const serviceID = String(getValue() ?? "");
              const label = options.serviceName?.(serviceID) ?? serviceID;
              return serviceID ? (
                <code
                  className="block max-w-36 truncate text-muted-foreground"
                  title={serviceID}
                >
                  {shortID(label)}
                </code>
              ) : (
                <span className="text-muted-foreground/45">—</span>
              );
            },
            header: "Service",
            size: 150,
          } satisfies ColumnDef<LogRecord>,
        ]
      : []),
    {
      accessorKey: "deploymentId",
      cell: ({ getValue }) => {
        const value = String(getValue());
        return (
          <code className="block max-w-36 truncate text-muted-foreground">
            {value ? shortID(value) : "—"}
          </code>
        );
      },
      header: ({ column }) => (
        <SortHeader label="Deployment" sorted={column.getIsSorted()} />
      ),
      size: 150,
    },
    {
      accessorKey: "text",
      cell: ({ row }) => (
        <div className="flex min-w-0 items-start gap-2">
          {row.original.traceId ? (
            <Braces
              aria-label="Correlated with a trace"
              className="mt-0.5 size-3 shrink-0 text-sky-500"
            />
          ) : null}
          <div className="min-w-0">
            <pre className="break-words whitespace-pre-wrap text-foreground">
              {row.original.phase === "before_deploy" ? (
                <span className="mr-2 inline-block border border-border bg-muted/35 px-1.5 py-0.5 align-middle text-[7px] tracking-[0.12em] text-muted-foreground uppercase">
                  Before deploy
                </span>
              ) : null}
              <AnsiText text={row.original.text} />
              {row.original.partial ? (
                <span className="ml-2 text-amber-500">[partial]</span>
              ) : null}
            </pre>
            <StructuredFields
              fields={row.original.fields}
              onAddFilter={onAddFilter}
            />
          </div>
        </div>
      ),
      header: ({ column }) => (
        <SortHeader label="Message" sorted={column.getIsSorted()} />
      ),
      size: 520,
    },
    {
      accessorKey: "traceId",
      cell: ({ getValue }) => {
        const value = String(getValue() ?? "");
        return value ? (
          <code className="text-sky-600 dark:text-sky-300">
            {shortID(value)}
          </code>
        ) : (
          <span className="text-muted-foreground/45">—</span>
        );
      },
      header: ({ column }) => (
        <SortHeader label="Trace" sorted={column.getIsSorted()} />
      ),
      size: 150,
    },
  ];
  return columns;
};
