import { ListFilter, Plus, Trash2, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { postgresFilterOperators } from "@/postgres-data-browser-model";
import type {
  PostgresFilterConnector,
  PostgresFilterOperator,
  PostgresTableFilter,
} from "@/postgres-data-browser-model";

const operatorNeedsValue = (operator: PostgresFilterOperator) =>
  operator !== "is null" && operator !== "is not null";

export const PostgresDataFilters = ({
  columns,
  filters,
  onAdd,
  onChange,
  onClear,
  onClose,
  onOpenInQuery,
  onRemove,
}: {
  columns: string[];
  filters: PostgresTableFilter[];
  onAdd: () => void;
  onChange: (filter: PostgresTableFilter) => void;
  onClear: () => void;
  onClose: () => void;
  onOpenInQuery?: () => void;
  onRemove: (id: string) => void;
}) => (
  <div className="shrink-0 overflow-x-auto border-b border-border bg-muted/10 px-3 py-2">
    <div className="flex min-w-max flex-col gap-1.5">
      {filters.map((filter, index) => (
        <div className="flex h-7 items-center gap-1.5" key={filter.id}>
          {index === 0 ? (
            <span className="w-14 px-2 text-[9px] text-muted-foreground">
              where
            </span>
          ) : (
            <select
              aria-label="Filter connector"
              className="h-7 w-14 border border-border bg-background px-1.5 text-[9px] outline-none focus:border-ring"
              onChange={(event) =>
                onChange({
                  ...filter,
                  connector: event.target.value as PostgresFilterConnector,
                })
              }
              value={filter.connector}
            >
              <option value="and">and</option>
              <option value="or">or</option>
            </select>
          )}
          <select
            aria-label="Filter column"
            className="h-7 w-40 border border-border bg-background px-1.5 text-[9px] outline-none focus:border-ring"
            onChange={(event) =>
              onChange({ ...filter, column: event.target.value })
            }
            value={filter.column}
          >
            {columns.map((column) => (
              <option key={column} value={column}>
                {column}
              </option>
            ))}
          </select>
          <select
            aria-label="Filter operator"
            className="h-7 w-32 border border-border bg-background px-1.5 text-[9px] outline-none focus:border-ring"
            onChange={(event) =>
              onChange({
                ...filter,
                operator: event.target.value as PostgresFilterOperator,
              })
            }
            value={filter.operator}
          >
            {postgresFilterOperators.map((operator) => (
              <option key={operator.value} value={operator.value}>
                {operator.label}
              </option>
            ))}
          </select>
          {operatorNeedsValue(filter.operator) ? (
            <Input
              aria-label="Filter value"
              className="h-7 w-52 text-[9px]"
              onChange={(event) =>
                onChange({ ...filter, value: event.target.value })
              }
              placeholder={
                filter.operator === "in" ? "one, two, three" : "Value"
              }
              value={filter.value}
            />
          ) : (
            <span className="w-52 px-2 text-[9px] text-muted-foreground">
              no value
            </span>
          )}
          <Button
            aria-label="Remove filter"
            onClick={() => onRemove(filter.id)}
            size="icon"
            variant="ghost"
          >
            <Trash2 />
          </Button>
        </div>
      ))}
      <div className="flex h-7 items-center gap-1.5">
        <Button
          disabled={columns.length === 0}
          onClick={onAdd}
          size="sm"
          variant="outline"
        >
          <Plus /> Add filter
        </Button>
        {onOpenInQuery ? (
          <Button onClick={onOpenInQuery} size="sm" variant="ghost">
            <ListFilter /> Open in Query
          </Button>
        ) : null}
        {filters.length > 0 ? (
          <Button onClick={onClear} size="sm" variant="ghost">
            Clear filters
          </Button>
        ) : null}
        <Button
          aria-label="Close filters"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>
    </div>
  </div>
);
