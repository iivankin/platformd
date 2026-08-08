import { AlertDialog } from "@base-ui/react/alert-dialog";
import {
  ChevronLeft,
  ChevronRight,
  ListFilter,
  Plus,
  RefreshCw,
  Trash2,
  X,
} from "lucide-react";
import { useState } from "react";
import type { ReactNode } from "react";

import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { PostgresDataAddRecordDialog } from "@/postgres-data-add-record";
import type { PostgresInsertRowHandler } from "@/postgres-data-add-record";
import {
  postgresIncomingRelations,
  postgresTablePageSize,
  rowPrimaryKey,
} from "@/postgres-data-browser-model";
import type {
  PostgresBrowserTable,
  PostgresColumnMeta,
  PostgresEnumType,
  PostgresForeignKey,
  PostgresTableRelation,
  PostgresTableSort,
} from "@/postgres-data-browser-model";
import { PostgresGridTable } from "@/postgres-data-grid-table";
import type {
  PostgresDeleteRowsHandler,
  PostgresUpdateCellHandler,
} from "@/postgres-data-grid-table";

type PostgresStatement = PostgresQueryResult["statements"][number];

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

const toolbarHint = ({
  hasPrimaryKey,
  sort,
  defaultOrder,
}: {
  defaultOrder: string;
  hasPrimaryKey: boolean;
  sort?: PostgresTableSort;
}) => {
  const base = hasPrimaryKey
    ? "Double-click a cell to edit"
    : "Browse and insert · edits need a primary key";
  if (sort) {
    return `${base} · sorted by ${sort.column} ${sort.direction}`;
  }
  return `${base}${defaultOrder}`;
};

const PostgresGridToolbar = ({
  approximateCount,
  columnsAvailable,
  deleting,
  filtersActive,
  loading,
  onAddRecord,
  onDeleteSelected,
  onPreviousPage,
  onRefresh,
  onRequestExactCount,
  onToggleFilters,
  page,
  range,
  selectedCount,
  selectedTable,
  sort,
}: {
  approximateCount: boolean;
  columnsAvailable: boolean;
  deleting: boolean;
  filtersActive: boolean;
  loading: boolean;
  onAddRecord: () => void;
  onDeleteSelected: () => void;
  onPreviousPage: () => void;
  onRefresh: () => void;
  onRequestExactCount: () => void;
  onToggleFilters: () => void;
  page: number;
  range: string;
  selectedCount: number;
  selectedTable?: PostgresBrowserTable;
  sort?: PostgresTableSort;
}) => {
  const canMutate = Boolean(selectedTable);
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
          {toolbarHint({
            defaultOrder,
            hasPrimaryKey: Boolean(selectedTable?.primaryKeyColumns.length),
            sort,
          })}
        </p>
      </div>
      <div className="ml-auto flex items-center gap-1">
        {selectedCount > 0 ? (
          <Button
            aria-label="Delete selected rows"
            disabled={deleting || loading}
            onClick={onDeleteSelected}
            size="sm"
            variant="outline"
          >
            <Trash2 />
            Delete {selectedCount.toLocaleString()}
          </Button>
        ) : (
          <Button
            aria-label="Add record"
            disabled={!canMutate || !columnsAvailable || loading}
            onClick={onAddRecord}
            size="sm"
            variant="outline"
          >
            <Plus />
            Add record
          </Button>
        )}
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

const DeleteRowsDialog = ({
  deleting,
  error,
  onConfirm,
  onOpenChange,
  open,
  selectedCount,
  tableName,
}: {
  deleting: boolean;
  error?: string;
  onConfirm: () => void;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  selectedCount: number;
  tableName: string;
}) => (
  <AlertDialog.Root
    onOpenChange={(nextOpen) => {
      if (deleting) {
        return;
      }
      onOpenChange(nextOpen);
    }}
    open={open}
  >
    <AlertDialog.Portal>
      <AlertDialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px]" />
      <AlertDialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
        <AlertDialog.Popup className="w-full max-w-md border border-border bg-background text-foreground shadow-2xl">
          <header className="flex items-start justify-between gap-4 border-b border-border px-4 py-3">
            <div>
              <AlertDialog.Title className="text-sm font-medium">
                Delete {selectedCount.toLocaleString()}{" "}
                {selectedCount === 1 ? "row" : "rows"}
              </AlertDialog.Title>
              <AlertDialog.Description className="mt-1 text-xs text-muted-foreground">
                Permanently remove the selected rows from {tableName}. This
                cannot be undone.
              </AlertDialog.Description>
            </div>
            <AlertDialog.Close
              aria-label="Close"
              className="grid size-8 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground"
              disabled={deleting}
            >
              <X className="size-4" />
            </AlertDialog.Close>
          </header>
          {error ? (
            <p className="border-b border-border px-4 py-3 text-[10px] text-destructive">
              {error}
            </p>
          ) : null}
          <footer className="flex justify-end gap-2 px-4 py-3">
            <AlertDialog.Close
              className="inline-flex h-8 items-center justify-center border border-border px-2.5 text-xs font-medium outline-none hover:bg-muted disabled:opacity-50"
              disabled={deleting}
            >
              Cancel
            </AlertDialog.Close>
            <Button
              disabled={deleting}
              onClick={onConfirm}
              size="sm"
              variant="outline"
            >
              {deleting ? "Deleting…" : "Delete"}
            </Button>
          </footer>
        </AlertDialog.Popup>
      </AlertDialog.Viewport>
    </AlertDialog.Portal>
  </AlertDialog.Root>
);

export const PostgresDataGrid = ({
  approximateCount,
  columnMeta,
  count,
  enums,
  error: loadError,
  filterPanel,
  filtersActive,
  foreignKeys,
  loading,
  onDeleteRows,
  onInsertRow,
  onNextPage,
  onPreviousPage,
  onRefresh,
  onRequestExactCount,
  onSort,
  onToggleFilters,
  onUpdateCell,
  page,
  postgresID,
  projectID,
  relations,
  selectedTable,
  sort,
  statement,
  tables,
}: {
  approximateCount: boolean;
  columnMeta: readonly PostgresColumnMeta[];
  count?: number;
  enums: ReadonlyMap<number, PostgresEnumType>;
  error?: string;
  filterPanel?: ReactNode;
  filtersActive: boolean;
  foreignKeys: PostgresForeignKey[];
  loading: boolean;
  onDeleteRows?: PostgresDeleteRowsHandler;
  onInsertRow?: PostgresInsertRowHandler;
  onNextPage: () => void;
  onPreviousPage: () => void;
  onRefresh: () => void;
  onRequestExactCount: () => void;
  onSort: (column: string, direction?: "asc" | "desc") => void;
  onToggleFilters: () => void;
  onUpdateCell?: PostgresUpdateCellHandler;
  page: number;
  postgresID: string;
  projectID: string;
  relations: PostgresTableRelation[];
  selectedTable?: PostgresBrowserTable;
  sort?: PostgresTableSort;
  statement?: PostgresStatement;
  tables: PostgresBrowserTable[];
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
  const tableID = selectedTable
    ? `${selectedTable.schema}\u0000${selectedTable.name}`
    : "";
  const [selection, setSelection] = useState<{
    keys: string[];
    page: number;
    tableID: string;
  }>({ keys: [], page: 0, tableID: "" });
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string>();
  const [addOpen, setAddOpen] = useState(false);
  const selectedRowKeys =
    selection.tableID === tableID && selection.page === page
      ? selection.keys
      : [];
  const setSelectedRowKeys = (keys: string[]) => {
    setSelection({ keys, page, tableID });
  };

  const confirmDelete = async () => {
    if (!(onDeleteRows && selectedTable && selectedRowKeys.length > 0)) {
      return;
    }
    const wanted = new Set(selectedRowKeys);
    const selectedRows = rows.flatMap((row) => {
      const values = rowColumnValues(columns, row);
      const key = rowPrimaryKey(selectedTable, values);
      return key && wanted.has(key) ? [values] : [];
    });
    if (selectedRows.length === 0) {
      return;
    }
    setDeleting(true);
    setDeleteError(undefined);
    try {
      await onDeleteRows({ rows: selectedRows, table: selectedTable });
      setSelectedRowKeys([]);
      setDeleteOpen(false);
    } catch (error) {
      setDeleteError(
        error instanceof Error ? error.message : "Unable to delete rows"
      );
    } finally {
      setDeleting(false);
    }
  };

  return (
    <section className="flex min-w-0 flex-col bg-background">
      <header className="flex h-12 shrink-0 items-center gap-3 border-b border-border px-3">
        <PostgresGridToolbar
          approximateCount={approximateCount}
          columnsAvailable={columns.length > 0}
          deleting={deleting}
          filtersActive={filtersActive}
          loading={loading}
          onAddRecord={() => setAddOpen(true)}
          onDeleteSelected={() => setDeleteOpen(true)}
          onPreviousPage={onPreviousPage}
          onRefresh={onRefresh}
          onRequestExactCount={onRequestExactCount}
          onToggleFilters={onToggleFilters}
          page={page}
          range={range}
          selectedCount={selectedRowKeys.length}
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
          columnMeta={columnMeta}
          enums={enums}
          foreignKeys={foreignKeys}
          loading={loading}
          onDeleteRows={onDeleteRows}
          onSelectedRowsChange={setSelectedRowKeys}
          onSort={onSort}
          onUpdateCell={onUpdateCell}
          page={page}
          postgresID={postgresID}
          projectID={projectID}
          relations={relations}
          selectedRowKeys={selectedRowKeys}
          selectedTable={selectedTable}
          sort={sort}
          statement={statement}
          tables={tables}
        />
      </div>

      <PostgresGridStatus
        columnCount={
          columns.length + postgresIncomingRelations(relations).length
        }
        error={loadError}
        range={range}
        rowCount={rows.length}
        truncated={truncated}
      />

      <DeleteRowsDialog
        deleting={deleting}
        error={deleteError}
        onConfirm={() => void confirmDelete()}
        onOpenChange={setDeleteOpen}
        open={deleteOpen}
        selectedCount={selectedRowKeys.length}
        tableName={
          selectedTable
            ? `${selectedTable.schema}.${selectedTable.name}`
            : "table"
        }
      />

      {selectedTable && onInsertRow ? (
        <PostgresDataAddRecordDialog
          columnMeta={columnMeta}
          columns={columns}
          enums={enums}
          onInsert={onInsertRow}
          onOpenChange={setAddOpen}
          open={addOpen}
          table={selectedTable}
        />
      ) : null}
    </section>
  );
};
