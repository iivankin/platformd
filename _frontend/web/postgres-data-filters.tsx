import { ListFilter, Plus, Trash2, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FieldSelect } from "@/field-select";
import { postgresFilterOperators } from "@/postgres-data-browser-model";
import type {
  PostgresFilterConnector,
  PostgresFilterOperator,
  PostgresTableFilter,
} from "@/postgres-data-browser-model";

const operatorNeedsValue = (operator: PostgresFilterOperator) =>
  operator !== "is null" && operator !== "is not null";

const filterColumnItems = (columns: string[], selected: string) => {
  let values = columns;
  if (columns.length === 0) {
    values = [selected || "__none__"];
  } else if (selected !== "" && !columns.includes(selected)) {
    values = [selected, ...columns];
  }
  return values.map((column) => ({
    label: column === "__none__" ? "No columns" : column,
    value: column,
  }));
};

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
            <FieldSelect
              aria-label="Filter connector"
              className="h-7 w-14 text-[9px]"
              items={[
                { label: "and", value: "and" },
                { label: "or", value: "or" },
              ]}
              onValueChange={(next) =>
                onChange({
                  ...filter,
                  connector: next as PostgresFilterConnector,
                })
              }
              size="sm"
              value={filter.connector}
            />
          )}
          <FieldSelect
            aria-label="Filter column"
            className="h-7 w-40 text-[9px]"
            items={filterColumnItems(columns, filter.column)}
            onValueChange={(next) => onChange({ ...filter, column: next })}
            size="sm"
            value={filter.column || (columns[0] ?? "__none__")}
          />
          <FieldSelect
            aria-label="Filter operator"
            className="h-7 w-32 text-[9px]"
            items={postgresFilterOperators.map((operator) => ({
              label: operator.label,
              value: operator.value,
            }))}
            onValueChange={(next) =>
              onChange({
                ...filter,
                operator: next as PostgresFilterOperator,
              })
            }
            size="sm"
            value={filter.operator}
          />
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
