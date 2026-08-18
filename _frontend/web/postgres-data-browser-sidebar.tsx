import { RefreshCw, Search, Table2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { FieldSelect } from "@/field-select";
import { cn } from "@/lib/utils";
import type { PostgresBrowserTable } from "@/postgres-data-browser-model";

const rowCountFormatter = new Intl.NumberFormat(undefined, {
  maximumFractionDigits: 1,
  notation: "compact",
});

const tableIdentity = (table: PostgresBrowserTable) =>
  `${table.schema}\u0000${table.name}`;

export const PostgresDataBrowserSidebar = ({
  loading,
  onRefresh,
  onSchemaChange,
  onSearchChange,
  onTableChange,
  schemas,
  search,
  selectedSchema,
  selectedTable,
  tableCount,
  tables,
}: {
  loading: boolean;
  onRefresh: () => void;
  onSchemaChange: (schema: string) => void;
  onSearchChange: (search: string) => void;
  onTableChange: (table: PostgresBrowserTable) => void;
  schemas: string[];
  search: string;
  selectedSchema: string;
  selectedTable?: PostgresBrowserTable;
  tableCount: number;
  tables: PostgresBrowserTable[];
}) => (
  <aside className="flex min-h-0 flex-col border-r border-border bg-muted/10">
    <div className="border-b border-border p-2">
      <label
        className="mb-1 block text-[8px] tracking-[0.1em] text-muted-foreground uppercase"
        htmlFor="postgres-schema"
      >
        Schema
      </label>
      <FieldSelect
        className="text-[10px]"
        disabled={loading || schemas.length === 0}
        id="postgres-schema"
        items={
          schemas.length === 0
            ? [
                {
                  label: selectedSchema || "No schemas",
                  value: selectedSchema || "__none__",
                },
              ]
            : schemas.map((schema) => ({ label: schema, value: schema }))
        }
        onValueChange={onSchemaChange}
        value={
          selectedSchema === "" && schemas.length === 0
            ? "__none__"
            : selectedSchema
        }
      />
    </div>
    <div className="flex gap-1 border-b border-border p-2">
      <label className="relative min-w-0 flex-1">
        <Search className="pointer-events-none absolute top-1/2 left-2 size-3 -translate-y-1/2 text-muted-foreground" />
        <input
          aria-label="Search PostgreSQL tables"
          className="h-8 w-full border border-border bg-background pr-2 pl-7 text-[10px] outline-none placeholder:text-muted-foreground focus:border-ring"
          onChange={(event) => onSearchChange(event.target.value)}
          placeholder="Search tables"
          value={search}
        />
      </label>
      <Button
        aria-label="Refresh PostgreSQL table list"
        disabled={loading}
        onClick={onRefresh}
        size="icon"
        variant="outline"
      >
        <RefreshCw className={cn(loading && "animate-spin")} />
      </Button>
    </div>
    <div className="min-h-0 flex-1 overflow-y-auto py-1">
      {tables.map((table) => {
        const selected =
          selectedTable &&
          tableIdentity(table) === tableIdentity(selectedTable);
        return (
          <button
            className={cn(
              "flex h-8 w-full items-center gap-2 border-l-2 border-transparent px-2 text-left text-[10px] text-muted-foreground transition-colors hover:bg-muted/50 hover:text-foreground",
              selected &&
                "border-foreground bg-muted/60 font-medium text-foreground"
            )}
            key={tableIdentity(table)}
            onClick={() => onTableChange(table)}
            type="button"
          >
            <Table2 className="size-3.5 shrink-0" />
            <span className="min-w-0 flex-1 truncate">{table.name}</span>
            {table.approximateRows === undefined ? null : (
              <span className="text-[8px] text-muted-foreground tabular-nums">
                ~{rowCountFormatter.format(table.approximateRows)}
              </span>
            )}
          </button>
        );
      })}
      {loading ? (
        <p className="px-3 py-4 text-[9px] text-muted-foreground">
          Reading schema…
        </p>
      ) : null}
      {!loading && tables.length === 0 ? (
        <p className="px-3 py-4 text-[9px] leading-4 text-muted-foreground">
          {search ? "No matching tables." : "No tables in this schema."}
        </p>
      ) : null}
    </div>
    <footer className="border-t border-border px-3 py-2 text-[8px] text-muted-foreground">
      {tables.length} of {tableCount} tables
    </footer>
  </aside>
);
