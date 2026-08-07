import { Menu } from "@base-ui/react/menu";
import {
  ArrowDown,
  ArrowDownUp,
  ArrowUp,
  ChevronLeft,
  ChevronRight,
  ListFilter,
  RefreshCw,
  Table2,
} from "lucide-react";
import type { AriaAttributes, ReactNode } from "react";

import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import {
  postgresIncomingRelations,
  postgresOutgoingRelationForColumn,
  postgresTablePageSize,
} from "@/postgres-data-browser-model";
import type {
  PostgresBrowserTable,
  PostgresTableRelation,
  PostgresTableSort,
} from "@/postgres-data-browser-model";
import { PostgresDataCell, PostgresRelationCell } from "@/postgres-data-cell";

type PostgresStatement = PostgresQueryResult["statements"][number];

const postgresTypeNames = new Map<number, string>([
  [16, "bool"],
  [17, "bytea"],
  [20, "int8"],
  [21, "int2"],
  [23, "int4"],
  [25, "text"],
  [114, "json"],
  [700, "float4"],
  [701, "float8"],
  [1042, "char"],
  [1043, "varchar"],
  [1082, "date"],
  [1114, "timestamp"],
  [1184, "timestamptz"],
  [1700, "numeric"],
  [2950, "uuid"],
  [3802, "jsonb"],
]);

const rowColumnValues = (
  columns: PostgresStatement["columns"],
  row: PostgresStatement["rows"][number]
) => {
  const values: Record<string, string | undefined> = {};
  for (const [index, column] of columns.entries()) {
    const cell = row[index];
    if (!cell || cell.null || cell.base64 !== undefined) {
      values[column.name] = undefined;
      continue;
    }
    values[column.name] = cell.text;
  }
  return values;
};

const columnAriaSort = (
  column: string,
  sort?: PostgresTableSort
): AriaAttributes["aria-sort"] => {
  if (sort?.column !== column) {
    return "none";
  }
  return sort.direction === "asc" ? "ascending" : "descending";
};

const SortIcon = ({
  column,
  sort,
}: {
  column: string;
  sort?: PostgresTableSort;
}) => {
  if (sort?.column !== column) {
    return <ArrowDownUp className="size-3 text-muted-foreground/60" />;
  }
  return sort.direction === "asc" ? (
    <ArrowUp className="size-3" />
  ) : (
    <ArrowDown className="size-3" />
  );
};

const GridEmptyState = ({
  loading,
  selectedTable,
}: {
  loading: boolean;
  selectedTable?: PostgresBrowserTable;
}) => {
  if (loading) {
    return (
      <div className="grid min-h-64 place-items-center text-[10px] text-muted-foreground">
        Loading rows…
      </div>
    );
  }
  if (selectedTable) {
    return (
      <div className="grid min-h-48 place-items-center text-[10px] text-muted-foreground">
        This table has no rows on this page.
      </div>
    );
  }
  return (
    <div className="grid min-h-64 place-items-center px-6 text-center">
      <div>
        <Table2 className="mx-auto size-5 text-muted-foreground" />
        <p className="mt-3 text-[10px] font-medium">No table selected</p>
        <p className="mt-1 text-[9px] text-muted-foreground">
          Choose a table from the schema browser.
        </p>
      </div>
    </div>
  );
};

const ColumnHeader = ({
  column,
  onSort,
  sort,
  typeOID,
}: {
  column: string;
  onSort: (column: string, direction?: "asc" | "desc") => void;
  sort?: PostgresTableSort;
  typeOID: number;
}) => {
  const activeSort = sort?.column === column;
  return (
    <Menu.Root>
      <Menu.Trigger
        className={cn(
          "flex w-full items-center gap-2 px-3 py-2 text-left outline-none hover:bg-muted/40 data-[popup-open]:bg-muted/40",
          activeSort && "bg-muted/30"
        )}
      >
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium">{column}</span>
          <span className="block text-[8px] font-normal text-muted-foreground">
            {postgresTypeNames.get(typeOID) ?? `oid ${typeOID.toString()}`}
          </span>
        </span>
        <SortIcon column={column} sort={sort} />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner align="start" className="z-50" sideOffset={2}>
          <Menu.Popup className="min-w-44 border border-border bg-popover p-1 text-[10px] text-popover-foreground shadow-lg">
            {sort ? (
              <Menu.Item
                className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
                onClick={() => onSort(column)}
              >
                <ArrowDownUp className="size-3.5" /> Clear sort
              </Menu.Item>
            ) : null}
            <Menu.Item
              className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
              onClick={() => onSort(column, "asc")}
            >
              <ArrowUp className="size-3.5" /> Sort Ascending
            </Menu.Item>
            <Menu.Item
              className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
              onClick={() => onSort(column, "desc")}
            >
              <ArrowDown className="size-3.5" /> Sort Descending
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
};

const RelationColumnHeader = ({ label }: { label: string }) => (
  <div className="px-3 py-2 text-left">
    <span className="block truncate font-medium">{label}</span>
    <span className="block text-[8px] font-normal text-muted-foreground">
      relation
    </span>
  </div>
);

const PostgresGridTable = ({
  loading,
  onOpenRelation,
  onSort,
  page,
  relations,
  selectedTable,
  sort,
  statement,
}: {
  loading: boolean;
  onOpenRelation: (
    relation: PostgresTableRelation,
    columnValues: Record<string, string | undefined>
  ) => void;
  onSort: (column: string, direction?: "asc" | "desc") => void;
  page: number;
  relations: PostgresTableRelation[];
  selectedTable?: PostgresBrowserTable;
  sort?: PostgresTableSort;
  statement?: PostgresStatement;
}) => {
  const columns = statement?.columns ?? [];
  const rows = statement?.rows.slice(0, postgresTablePageSize) ?? [];
  const incoming = postgresIncomingRelations(relations);
  if (!(selectedTable && columns.length > 0)) {
    return <GridEmptyState loading={loading} selectedTable={selectedTable} />;
  }
  return (
    <>
      <table className="w-full min-w-max border-collapse text-left text-[10px]">
        <thead className="sticky top-0 z-10 bg-background">
          <tr className="border-b border-border">
            <th className="sticky left-0 z-20 w-11 border-r border-border bg-muted/30 px-2 py-2 text-right text-[8px] font-normal text-muted-foreground">
              #
            </th>
            {columns.map((column, columnIndex) => (
              <th
                aria-sort={columnAriaSort(column.name, sort)}
                className="min-w-40 border-r border-border p-0 last:border-r-0"
                key={`${columnIndex.toString()}:${column.name}`}
              >
                <ColumnHeader
                  column={column.name}
                  onSort={onSort}
                  sort={sort}
                  typeOID={column.typeOid}
                />
              </th>
            ))}
            {incoming.map((relation) => (
              <th
                className="min-w-36 border-r border-border bg-muted/10 p-0 last:border-r-0"
                key={relation.key}
              >
                <RelationColumnHeader label={relation.label} />
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((row, rowIndex) => {
            const columnValues = rowColumnValues(columns, row);
            return (
              <tr
                className="border-b border-border last:border-b-0 hover:bg-muted/20"
                key={(page * postgresTablePageSize + rowIndex).toString()}
              >
                <td className="sticky left-0 border-r border-border bg-background px-2 py-2 text-right text-[8px] text-muted-foreground tabular-nums">
                  {page * postgresTablePageSize + rowIndex + 1}
                </td>
                {row.map((cell, cellIndex) => {
                  const column = columns[cellIndex];
                  if (!column) {
                    return null;
                  }
                  const outgoing = postgresOutgoingRelationForColumn(
                    relations,
                    column.name
                  );
                  const canOpenOutgoing =
                    outgoing &&
                    outgoing.foreignKey.columns.every(
                      (name) => columnValues[name] !== undefined
                    );
                  return (
                    <td
                      className="max-w-96 border-r border-border p-0 last:border-r-0"
                      key={cellIndex.toString()}
                    >
                      <PostgresDataCell
                        cell={cell}
                        column={column.name}
                        onOpenRelation={
                          canOpenOutgoing && outgoing
                            ? () => onOpenRelation(outgoing, columnValues)
                            : undefined
                        }
                        relationLabel={outgoing?.label}
                        typeOID={column.typeOid}
                      />
                    </td>
                  );
                })}
                {incoming.map((relation) => {
                  const canOpen = relation.foreignKey.foreignColumns.every(
                    (name) => columnValues[name] !== undefined
                  );
                  return (
                    <td
                      className="border-r border-border bg-muted/5 p-0 last:border-r-0"
                      key={relation.key}
                    >
                      {canOpen ? (
                        <PostgresRelationCell
                          label={relation.label}
                          onOpen={() => onOpenRelation(relation, columnValues)}
                        />
                      ) : (
                        <div className="px-3 py-2 text-muted-foreground italic">
                          —
                        </div>
                      )}
                    </td>
                  );
                })}
              </tr>
            );
          })}
        </tbody>
      </table>
      {rows.length === 0 ? (
        <GridEmptyState loading={loading} selectedTable={selectedTable} />
      ) : null}
    </>
  );
};

const postgresRangeLabel = ({
  approximateCount,
  count,
  loading,
  page,
  rowCount,
}: {
  approximateCount: boolean;
  count?: number;
  loading: boolean;
  page: number;
  rowCount: number;
}) => {
  if (loading) {
    return "Loading…";
  }
  const firstVisibleRow = rowCount > 0 ? page * postgresTablePageSize + 1 : 0;
  const lastVisibleRow = page * postgresTablePageSize + rowCount;
  const countLabel = count === undefined ? "?" : count.toLocaleString();
  return `${firstVisibleRow.toLocaleString()} - ${lastVisibleRow.toLocaleString()} of ${approximateCount ? "~" : ""}${countLabel}`;
};

const PostgresGridToolbar = ({
  approximateCount,
  columnsAvailable,
  filtersActive,
  loading,
  onPreviousPage,
  onRefresh,
  onRequestExactCount,
  onToggleFilters,
  page,
  range,
  selectedTable,
  sort,
}: {
  approximateCount: boolean;
  columnsAvailable: boolean;
  filtersActive: boolean;
  loading: boolean;
  onPreviousPage: () => void;
  onRefresh: () => void;
  onRequestExactCount: () => void;
  onToggleFilters: () => void;
  page: number;
  range: string;
  selectedTable?: PostgresBrowserTable;
  sort?: PostgresTableSort;
}) => {
  const defaultOrder =
    !sort && selectedTable?.primaryKeyColumns.length
      ? ` · ordered by ${selectedTable.primaryKeyColumns.join(", ")}`
      : "";
  return (
    <>
      <div className="min-w-0">
        <div className="flex items-center gap-1.5 text-[10px] font-medium">
          <span className="text-muted-foreground">
            {selectedTable?.schema ?? "schema"}
          </span>
          <span className="text-muted-foreground/50">/</span>
          <span className="truncate">{selectedTable?.name ?? "table"}</span>
        </div>
        <p className="mt-0.5 text-[8px] text-muted-foreground">
          Read-only browser
          {sort
            ? ` · sorted by ${sort.column} ${sort.direction}`
            : defaultOrder}
        </p>
      </div>
      <div className="ml-auto flex items-center gap-1">
        <Button
          aria-label="Toggle PostgreSQL table filters"
          className="relative"
          disabled={!selectedTable || !columnsAvailable}
          onClick={onToggleFilters}
          size="icon"
          variant="ghost"
        >
          <ListFilter />
          {filtersActive ? (
            <span className="absolute top-1 right-1 size-1.5 bg-foreground" />
          ) : null}
        </Button>
        <button
          className="mr-2 hidden text-[8px] text-muted-foreground tabular-nums hover:text-foreground lg:inline"
          disabled={!approximateCount}
          onClick={onRequestExactCount}
          title={approximateCount ? "Calculate exact row count" : undefined}
          type="button"
        >
          {range}
        </button>
        <Button
          aria-label="Refresh PostgreSQL table rows"
          disabled={loading || !selectedTable}
          onClick={onRefresh}
          size="icon"
          variant="ghost"
        >
          <RefreshCw className={cn(loading && "animate-spin")} />
        </Button>
        <Button
          aria-label="Previous PostgreSQL table page"
          disabled={loading || page === 0}
          onClick={onPreviousPage}
          size="icon"
          variant="ghost"
        >
          <ChevronLeft />
        </Button>
      </div>
    </>
  );
};

const PostgresGridStatus = ({
  columnCount,
  error,
  range,
  rowCount,
  truncated,
}: {
  columnCount: number;
  error?: string;
  range: string;
  rowCount: number;
  truncated: boolean;
}) => {
  if (error || truncated) {
    return (
      <p
        aria-live="polite"
        className={cn(
          "shrink-0 border-t border-border px-3 py-2 text-[9px]",
          error ? "text-destructive" : "text-amber-700 dark:text-amber-300"
        )}
      >
        {error ??
          "The response reached the safe size limit. Narrow the result with filters or open it in Query."}
      </p>
    );
  }
  return (
    <footer className="flex h-8 shrink-0 items-center justify-between border-t border-border px-3 text-[8px] text-muted-foreground">
      <span>
        {rowCount.toLocaleString()} rows loaded · {columnCount} columns
      </span>
      <span className="lg:hidden">{range}</span>
    </footer>
  );
};

export const PostgresDataGrid = ({
  approximateCount,
  count,
  error,
  filterPanel,
  filtersActive,
  loading,
  onNextPage,
  onOpenRelation,
  onPreviousPage,
  onRefresh,
  onRequestExactCount,
  onSort,
  onToggleFilters,
  page,
  relations,
  selectedTable,
  sort,
  statement,
}: {
  approximateCount: boolean;
  count?: number;
  error?: string;
  filterPanel?: ReactNode;
  filtersActive: boolean;
  loading: boolean;
  onNextPage: () => void;
  onOpenRelation: (
    relation: PostgresTableRelation,
    columnValues: Record<string, string | undefined>
  ) => void;
  onPreviousPage: () => void;
  onRefresh: () => void;
  onRequestExactCount: () => void;
  onSort: (column: string, direction?: "asc" | "desc") => void;
  onToggleFilters: () => void;
  page: number;
  relations: PostgresTableRelation[];
  selectedTable?: PostgresBrowserTable;
  sort?: PostgresTableSort;
  statement?: PostgresStatement;
}) => {
  const columns = statement?.columns ?? [];
  const rows = statement?.rows.slice(0, postgresTablePageSize) ?? [];
  const hasNextPage = (statement?.rows.length ?? 0) > postgresTablePageSize;
  const truncated = Boolean(statement?.truncated);
  const range = postgresRangeLabel({
    approximateCount,
    count,
    loading,
    page,
    rowCount: rows.length,
  });

  return (
    <section className="flex min-w-0 flex-col bg-background">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-border px-3">
        <PostgresGridToolbar
          approximateCount={approximateCount}
          columnsAvailable={columns.length > 0}
          filtersActive={filtersActive}
          loading={loading}
          onPreviousPage={onPreviousPage}
          onRefresh={onRefresh}
          onRequestExactCount={onRequestExactCount}
          onToggleFilters={onToggleFilters}
          page={page}
          range={range}
          selectedTable={selectedTable}
          sort={sort}
        />
        <div className="flex items-center">
          <Button
            aria-label="Next PostgreSQL table page"
            disabled={loading || !hasNextPage}
            onClick={onNextPage}
            size="icon"
            variant="ghost"
          >
            <ChevronRight />
          </Button>
        </div>
      </header>

      {filterPanel}

      <div className="min-h-0 flex-1 overflow-auto">
        <PostgresGridTable
          loading={loading}
          onOpenRelation={onOpenRelation}
          onSort={onSort}
          page={page}
          relations={relations}
          selectedTable={selectedTable}
          sort={sort}
          statement={statement}
        />
      </div>

      <PostgresGridStatus
        columnCount={
          columns.length + postgresIncomingRelations(relations).length
        }
        error={error}
        range={range}
        rowCount={rows.length}
        truncated={truncated}
      />
    </section>
  );
};
