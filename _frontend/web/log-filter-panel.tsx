import { Plus, RotateCcw, X } from "lucide-react";
import { useState } from "react";

import type { LogRecord } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { logFieldFilterSchema } from "@/log-field-filter";
import type {
  LogFieldFilter,
  LogFieldFilterOperator,
} from "@/log-field-filter";
import { atLeastLogSeverity, logSeverityOptions } from "@/log-severity";
import type { LogSeverity } from "@/log-severity";

const operatorOptions: {
  label: string;
  value: LogFieldFilterOperator;
}[] = [
  { label: "equals", value: "equals" },
  { label: "contains", value: "contains" },
  { label: "exists", value: "exists" },
];

const operatorItems = Object.fromEntries(
  operatorOptions.map((option) => [option.value, option.label])
);

const filterLabel = (filter: LogFieldFilter) =>
  filter.operator === "exists"
    ? `${filter.path} exists`
    : `${filter.path} ${filter.operator === "equals" ? "=" : "contains"} ${filter.value}`;

export const LogFilterChips = ({
  deploymentID,
  fieldFilters,
  onRemoveDeployment,
  onRemoveFieldFilter,
  onRemoveSpan,
  onRemoveTrace,
  severity,
  spanID,
  traceID,
}: {
  deploymentID?: string;
  fieldFilters: LogFieldFilter[];
  onRemoveDeployment?: () => void;
  onRemoveFieldFilter: (index: number) => void;
  onRemoveSpan?: () => void;
  onRemoveTrace?: () => void;
  severity: LogSeverity;
  spanID?: string;
  traceID?: string;
}) => (
  <div className="flex min-w-0 flex-wrap items-center gap-2">
    <span className="flex h-8 shrink-0 items-center border border-border bg-muted/25 px-2 text-[8px] tracking-[0.04em] text-foreground uppercase">
      Level · {severity === "all" ? "all" : `${severity}+`}
    </span>
    {deploymentID ? (
      <span className="flex h-8 max-w-56 shrink-0 items-center gap-1 border border-border px-2 text-[8px] text-muted-foreground">
        <span className="truncate">Deployment · {deploymentID}</span>
        {onRemoveDeployment ? (
          <button
            aria-label="Remove deployment filter"
            className="hover:text-foreground"
            onClick={onRemoveDeployment}
            type="button"
          >
            <X className="size-2.5" />
          </button>
        ) : null}
      </span>
    ) : null}
    {traceID ? (
      <span className="flex h-8 max-w-64 shrink-0 items-center gap-1 border border-violet-500/35 bg-violet-500/5 px-2 font-mono text-[8px] text-violet-700 dark:text-violet-300">
        <span className="truncate">Trace · {traceID}</span>
        {onRemoveTrace ? (
          <button
            aria-label="Remove trace filter"
            className="hover:text-foreground"
            onClick={onRemoveTrace}
            type="button"
          >
            <X className="size-2.5" />
          </button>
        ) : null}
      </span>
    ) : null}
    {spanID ? (
      <span className="flex h-8 max-w-52 shrink-0 items-center gap-1 border border-violet-500/35 bg-violet-500/5 px-2 font-mono text-[8px] text-violet-700 dark:text-violet-300">
        <span className="truncate">Span · {spanID}</span>
        {onRemoveSpan ? (
          <button
            aria-label="Remove span filter"
            className="hover:text-foreground"
            onClick={onRemoveSpan}
            type="button"
          >
            <X className="size-2.5" />
          </button>
        ) : null}
      </span>
    ) : null}
    {fieldFilters.map((filter, index) => (
      <span
        className="flex h-8 max-w-72 shrink-0 items-center gap-1 border border-sky-500/35 bg-sky-500/5 px-2 text-[8px] text-sky-700 dark:text-sky-300"
        key={`${filter.path}:${filter.operator}:${filter.value}:${index.toString()}`}
      >
        <span className="truncate">{filterLabel(filter)}</span>
        <button
          aria-label={`Remove ${filter.path} filter`}
          className="hover:text-foreground"
          onClick={() => onRemoveFieldFilter(index)}
          type="button"
        >
          <X className="size-2.5" />
        </button>
      </span>
    ))}
  </div>
);

export const LogFilterPanel = ({
  fieldFilters,
  onAddFieldFilter,
  onReset,
  onSeverityChange,
  records,
  severity,
  structured,
}: {
  fieldFilters: LogFieldFilter[];
  onAddFieldFilter: (filter: LogFieldFilter) => void;
  onReset: () => void;
  onSeverityChange: (severity: LogSeverity) => void;
  records: LogRecord[];
  severity: LogSeverity;
  structured: boolean;
}) => {
  const [path, setPath] = useState("");
  const [operator, setOperator] = useState<LogFieldFilterOperator>("equals");
  const [value, setValue] = useState("");
  const candidate = logFieldFilterSchema.safeParse({ operator, path, value });
  const canAdd =
    structured &&
    candidate.success &&
    fieldFilters.length < 8 &&
    (operator !== "contains" || value.length > 0) &&
    (operator !== "exists" || value.length === 0);
  const severityItems = Object.fromEntries(
    logSeverityOptions.map((option) => [
      option.value,
      `${option.label} · ${records
        .filter((record) => atLeastLogSeverity(record, option.value))
        .length.toString()}`,
    ])
  );

  return (
    <div className="grid border-b border-border lg:grid-cols-[minmax(12rem,0.7fr)_minmax(28rem,2fr)_auto]">
      <div className="flex min-h-14 items-center gap-3 border-r border-border px-3 max-lg:border-r-0 max-lg:border-b">
        <span className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
          Level
        </span>
        <Select
          items={severityItems}
          onValueChange={(selectedSeverity) =>
            onSeverityChange(selectedSeverity as LogSeverity)
          }
          value={severity}
        >
          <SelectTrigger
            aria-label="Minimum log level"
            className="h-8 min-w-0 flex-1 text-[9px]"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            {logSeverityOptions.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {severityItems[option.value]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <form
        className={cn(
          "grid min-h-14 grid-cols-[minmax(8rem,1fr)_8rem_minmax(8rem,1fr)_auto] items-center gap-2 border-r border-border px-3 max-lg:border-r-0 max-lg:border-b max-sm:grid-cols-1 max-sm:py-3",
          !structured && "opacity-45"
        )}
        onSubmit={(event) => {
          event.preventDefault();
          if (!(canAdd && candidate.success)) {
            return;
          }
          onAddFieldFilter(candidate.data);
          setPath("");
          setValue("");
        }}
      >
        <Input
          aria-label="Structured log field path"
          className="h-8 text-[9px]"
          disabled={!structured}
          maxLength={256}
          onChange={(event) => setPath(event.target.value.trim())}
          placeholder={
            structured
              ? "caller or http.status_code"
              : "Structured service logs only"
          }
          title="Displayed text uses the top-level message field, then msg; otherwise the full JSON body is shown"
          value={path}
        />
        <Select
          items={operatorItems}
          disabled={!structured}
          onValueChange={(selectedOperator) => {
            const next = selectedOperator as LogFieldFilterOperator;
            setOperator(next);
            if (next === "exists") {
              setValue("");
            }
          }}
          value={operator}
        >
          <SelectTrigger
            aria-label="Structured log filter operator"
            className="h-8 w-full text-[9px]"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            {operatorOptions.map((option) => (
              <SelectItem key={option.value} value={option.value}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          aria-label="Structured log filter value"
          className="h-8 text-[9px]"
          disabled={!structured || operator === "exists"}
          maxLength={512}
          onChange={(event) => setValue(event.target.value)}
          placeholder={operator === "exists" ? "No value" : "value"}
          value={value}
        />
        <Button disabled={!canAdd} size="icon" type="submit" variant="outline">
          <Plus />
          <span className="sr-only">Add structured field filter</span>
        </Button>
      </form>
      <div className="flex min-h-14 items-center justify-center px-2">
        <Button
          aria-label="Reset log filters"
          onClick={onReset}
          size="icon"
          variant="ghost"
        >
          <RotateCcw />
        </Button>
      </div>
    </div>
  );
};
