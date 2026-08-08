import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import {
  ArrowDown,
  ArrowDownUp,
  ArrowRight,
  ArrowUp,
  ArrowUpLeft,
  ChevronLeft,
  ChevronRight,
  RefreshCw,
  Table2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type { AriaAttributes, ReactNode } from "react";

import { queryManagedPostgres } from "@/api";
import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { cn } from "@/lib/utils";
import {
  postgresCellEditorKind,
  postgresIncomingRelations,
  postgresOutgoingRelationForColumn,
  postgresRelationNavigation,
  postgresRelationsForTable,
  postgresTableDataSQL,
  postgresTablePageSize,
  postgresTypeLabel,
  rowPrimaryKey,
} from "@/postgres-data-browser-model";
import type {
  PostgresBrowserTable,
  PostgresCellEditValue,
  PostgresCellEditorKind,
  PostgresColumnMeta,
  PostgresEnumType,
  PostgresForeignKey,
  PostgresTableRelation,
  PostgresTableSort,
} from "@/postgres-data-browser-model";
import { PostgresDataCell } from "@/postgres-data-cell";

type PostgresStatement = PostgresQueryResult["statements"][number];

const errorMessage = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const tableIdentity = (table: PostgresBrowserTable) =>
  `${table.schema}\u0000${table.name}`;

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
  relationLabel,
  sort,
  typeLabel,
}: {
  column: string;
  onSort: (column: string, direction?: "asc" | "desc") => void;
  relationLabel?: string;
  sort?: PostgresTableSort;
  typeLabel: string;
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
          <span className="block truncate text-[8px] font-normal text-muted-foreground">
            {relationLabel ? `→ ${relationLabel} · ${typeLabel}` : typeLabel}
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

const rangeLabel = ({
  loading,
  page,
  rowCount,
}: {
  loading: boolean;
  page: number;
  rowCount: number;
}) => {
  if (loading) {
    return "Loading…";
  }
  if (rowCount === 0) {
    return "0 rows";
  }
  const first = page * postgresTablePageSize + 1;
  const last = page * postgresTablePageSize + rowCount;
  return `${first.toLocaleString()} - ${last.toLocaleString()}`;
};

const filtersLabel = (count: number) =>
  count === 1
    ? "1 relation filter"
    : `${count.toLocaleString()} relation filters`;

/* Mutual recursion: relation popovers embed the same grid table. */
/* eslint-disable no-use-before-define */

export type PostgresUpdateCellHandler = (input: {
  column: string;
  kind: PostgresCellEditorKind;
  rowValues: Record<string, string | undefined>;
  table: PostgresBrowserTable;
  value: PostgresCellEditValue;
}) => Promise<void>;

export type PostgresDeleteRowsHandler = (input: {
  rows: Record<string, string | undefined>[];
  table: PostgresBrowserTable;
}) => Promise<void>;

interface RelationPreviewProps {
  columnValues: Record<string, string | undefined>;
  enums: ReadonlyMap<number, PostgresEnumType>;
  foreignKeys: PostgresForeignKey[];
  label: string;
  onDeleteRows?: PostgresDeleteRowsHandler;
  onUpdateCell?: PostgresUpdateCellHandler;
  postgresID: string;
  projectID: string;
  relation: PostgresTableRelation;
  tables: PostgresBrowserTable[];
}

interface RelationLoadState {
  error?: string;
  key: string;
  loading: boolean;
  statement?: PostgresStatement;
}

const useRelationTableQuery = ({
  navigation,
  open,
  page,
  postgresID,
  projectID,
  refreshVersion,
  sort,
  target,
}: {
  navigation?: ReturnType<typeof postgresRelationNavigation>;
  open: boolean;
  page: number;
  postgresID: string;
  projectID: string;
  refreshVersion: number;
  sort?: PostgresTableSort;
  target?: PostgresBrowserTable;
}) => {
  const requestKey = JSON.stringify([
    navigation?.filters,
    page,
    postgresID,
    projectID,
    refreshVersion,
    sort,
    target ? tableIdentity(target) : "",
  ]);
  const [loadState, setLoadState] = useState<RelationLoadState>({
    key: "",
    loading: false,
  });

  useEffect(() => {
    if (!(open && navigation && target)) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      setLoadState({ key: requestKey, loading: true });
      try {
        const result = await queryManagedPostgres(
          projectID,
          postgresID,
          postgresTableDataSQL({
            filters: navigation.filters,
            page,
            sort,
            table: target,
          }),
          controller.signal
        );
        if (controller.signal.aborted) {
          return;
        }
        setLoadState({
          key: requestKey,
          loading: false,
          statement: result.statements[0],
        });
      } catch (loadError) {
        if (loadError instanceof Error && loadError.name === "AbortError") {
          return;
        }
        setLoadState({
          error: errorMessage(loadError, "Unable to load related rows"),
          key: requestKey,
          loading: false,
        });
      }
    };
    void load();
    return () => controller.abort();
  }, [navigation, open, page, postgresID, projectID, requestKey, sort, target]);

  const current = loadState.key === requestKey ? loadState : undefined;
  return {
    current,
    loading: Boolean(open && (!current || current.loading)),
    requestKey,
  };
};

const RelationPreviewBody = ({
  current,
  enums,
  foreignKeys,
  loading,
  navigation,
  onDeleteRows,
  onSort,
  onUpdateCell,
  page,
  postgresID,
  projectID,
  setPage,
  sort,
  tables,
  target,
  targetRelations,
}: {
  current?: RelationLoadState;
  enums: ReadonlyMap<number, PostgresEnumType>;
  foreignKeys: PostgresForeignKey[];
  loading: boolean;
  navigation?: ReturnType<typeof postgresRelationNavigation>;
  onDeleteRows?: PostgresDeleteRowsHandler;
  onSort: (column: string, direction?: "asc" | "desc") => void;
  onUpdateCell?: PostgresUpdateCellHandler;
  page: number;
  postgresID: string;
  projectID: string;
  setPage: (updater: (value: number) => number) => void;
  sort?: PostgresTableSort;
  tables: PostgresBrowserTable[];
  target?: PostgresBrowserTable;
  targetRelations: PostgresTableRelation[];
}) => {
  if (!(target && navigation)) {
    return (
      <div className="grid h-full min-h-48 place-items-center gap-2 px-3 text-center text-[9px] text-muted-foreground">
        <Table2 className="size-4" />
        Related table is not in the catalog.
      </div>
    );
  }
  if (current?.error) {
    return (
      <div className="grid h-full min-h-48 place-items-center px-3 text-center text-[9px] text-destructive">
        {current.error}
      </div>
    );
  }
  return (
    <PostgresGridTable
      enums={enums}
      foreignKeys={foreignKeys}
      loading={loading}
      onDeleteRows={onDeleteRows}
      onSort={(column, direction) => {
        onSort(column, direction);
        setPage(() => 0);
      }}
      onUpdateCell={onUpdateCell}
      page={page}
      postgresID={postgresID}
      projectID={projectID}
      relations={targetRelations}
      selectedTable={target}
      sort={sort}
      statement={current?.statement}
      tables={tables}
    />
  );
};

const withPreviewRefresh = <T,>(
  handler: ((input: T) => Promise<void>) | undefined,
  refresh: () => void
) =>
  handler
    ? async (input: T) => {
        await handler(input);
        refresh();
      }
    : undefined;

const PostgresRelationPreview = ({
  columnValues,
  enums,
  foreignKeys,
  onDeleteRows,
  onUpdateCell,
  postgresID,
  projectID,
  relation,
  tables,
  triggerClassName,
  triggerLabel,
  triggerTitle,
}: {
  columnValues: Record<string, string | undefined>;
  enums: ReadonlyMap<number, PostgresEnumType>;
  foreignKeys: PostgresForeignKey[];
  onDeleteRows?: PostgresDeleteRowsHandler;
  onUpdateCell?: PostgresUpdateCellHandler;
  postgresID: string;
  projectID: string;
  relation: PostgresTableRelation;
  tables: PostgresBrowserTable[];
  triggerClassName?: string;
  triggerLabel: ReactNode;
  triggerTitle?: string;
}) => {
  const [open, setOpen] = useState(false);
  const [page, setPage] = useState(0);
  const [sort, setSort] = useState<PostgresTableSort>();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const columnValuesKey = JSON.stringify(columnValues);
  const navigation = useMemo(
    () =>
      postgresRelationNavigation({
        columnValues: JSON.parse(columnValuesKey) as Record<
          string,
          string | undefined
        >,
        relation,
      }),
    [columnValuesKey, relation]
  );
  const target = useMemo(() => {
    if (!navigation) {
      return;
    }
    return tables.find(
      (table) =>
        table.schema === navigation.schema && table.name === navigation.table
    );
  }, [navigation, tables]);
  const targetRelations = useMemo(
    () => (target ? postgresRelationsForTable(foreignKeys, target) : []),
    [foreignKeys, target]
  );
  const { current, loading } = useRelationTableQuery({
    navigation,
    open,
    page,
    postgresID,
    projectID,
    refreshVersion,
    sort,
    target,
  });
  const refreshPreview = () => setRefreshVersion((value) => value + 1);
  const updateRelatedCell = withPreviewRefresh(onUpdateCell, refreshPreview);
  const deleteRelatedRows = withPreviewRefresh(onDeleteRows, refreshPreview);
  const rows = current?.statement?.rows.slice(0, postgresTablePageSize) ?? [];
  const hasNextPage =
    (current?.statement?.rows.length ?? 0) > postgresTablePageSize;
  const DirectionIcon =
    relation.direction === "outgoing" ? ArrowRight : ArrowUpLeft;

  return (
    <Popover.Root
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (!nextOpen) {
          setPage(0);
          setSort(undefined);
        }
      }}
      open={open}
    >
      <Popover.Trigger
        className={triggerClassName}
        disabled={!navigation || !target}
        title={triggerTitle}
        type="button"
      >
        {triggerLabel}
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner
          align="start"
          className="z-50"
          side="bottom"
          sideOffset={6}
        >
          <Popover.Popup className="flex h-[min(36rem,calc(100vh-4rem))] w-[min(72rem,calc(100vw-2rem))] flex-col border border-border bg-popover text-popover-foreground shadow-lg outline-none">
            <header className="flex h-10 shrink-0 items-center gap-2 border-b border-border px-3">
              <DirectionIcon className="size-3.5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <Popover.Title className="truncate text-[10px] font-medium">
                  {target ? `${target.schema}.${target.name}` : relation.label}
                </Popover.Title>
                <Popover.Description className="truncate text-[8px] text-muted-foreground">
                  {relation.direction === "outgoing"
                    ? "Parent table"
                    : "Related table"}
                  {" · "}
                  {rangeLabel({ loading, page, rowCount: rows.length })}
                </Popover.Description>
              </div>
              <Button
                aria-label="Previous related page"
                disabled={loading || page === 0}
                onClick={() => setPage((value) => Math.max(0, value - 1))}
                size="icon"
                variant="ghost"
              >
                <ChevronLeft />
              </Button>
              <Button
                aria-label="Refresh related table"
                disabled={loading || !target}
                onClick={() => setRefreshVersion((value) => value + 1)}
                size="icon"
                variant="ghost"
              >
                <RefreshCw />
              </Button>
              <Button
                aria-label="Next related page"
                disabled={loading || !hasNextPage}
                onClick={() => setPage((value) => value + 1)}
                size="icon"
                variant="ghost"
              >
                <ChevronRight />
              </Button>
              <Popover.Close
                aria-label="Close relation preview"
                className="grid size-7 place-items-center text-muted-foreground hover:text-foreground"
              >
                <X className="size-3.5" />
              </Popover.Close>
            </header>
            <div className="min-h-0 flex-1 overflow-auto">
              <RelationPreviewBody
                current={current}
                enums={enums}
                foreignKeys={foreignKeys}
                loading={loading}
                navigation={navigation}
                onDeleteRows={deleteRelatedRows}
                onSort={(column, direction) => {
                  setSort(direction ? { column, direction } : undefined);
                }}
                onUpdateCell={updateRelatedCell}
                page={page}
                postgresID={postgresID}
                projectID={projectID}
                setPage={setPage}
                sort={sort}
                tables={tables}
                target={target}
                targetRelations={targetRelations}
              />
            </div>
            <footer className="flex h-8 shrink-0 items-center justify-between border-t border-border px-3 text-[8px] text-muted-foreground">
              <span>
                {rows.length.toLocaleString()} rows loaded ·{" "}
                {(current?.statement?.columns.length ?? 0).toLocaleString()}{" "}
                columns
              </span>
              <span>{filtersLabel(navigation?.filters.length ?? 0)}</span>
            </footer>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
};

export const PostgresRelationOutgoingTrigger = ({
  columnValues,
  enums,
  foreignKeys,
  label,
  onDeleteRows,
  onUpdateCell,
  postgresID,
  projectID,
  relation,
  tables,
}: RelationPreviewProps) => (
  <PostgresRelationPreview
    columnValues={columnValues}
    enums={enums}
    foreignKeys={foreignKeys}
    onDeleteRows={onDeleteRows}
    onUpdateCell={onUpdateCell}
    postgresID={postgresID}
    projectID={projectID}
    relation={relation}
    tables={tables}
    triggerClassName="flex shrink-0 items-center gap-0.5 border-l border-border bg-muted/20 px-1.5 text-[9px] font-medium text-muted-foreground hover:bg-muted/50 hover:text-foreground disabled:opacity-40"
    triggerLabel={
      <>
        <ArrowRight className="size-3" />
        <span className="max-w-20 truncate">{label}</span>
      </>
    }
    triggerTitle={`Open ${label}`}
  />
);

export const PostgresRelationIncomingTrigger = ({
  columnValues,
  enums,
  foreignKeys,
  label,
  onDeleteRows,
  onUpdateCell,
  postgresID,
  projectID,
  relation,
  tables,
}: RelationPreviewProps) => (
  <div className="flex h-full min-h-8 items-center px-2">
    <PostgresRelationPreview
      columnValues={columnValues}
      enums={enums}
      foreignKeys={foreignKeys}
      onDeleteRows={onDeleteRows}
      onUpdateCell={onUpdateCell}
      postgresID={postgresID}
      projectID={projectID}
      relation={relation}
      tables={tables}
      triggerClassName="max-w-full truncate border border-border bg-muted/30 px-2 py-1 text-[9px] font-medium hover:bg-muted/60 disabled:opacity-40"
      triggerLabel={label}
      triggerTitle={`Open related ${label}`}
    />
  </div>
);

const emptySelectedRowKeys: readonly string[] = [];
const emptyColumnMeta: readonly PostgresColumnMeta[] = [];

export const PostgresGridTable = ({
  columnMeta = emptyColumnMeta,
  enums,
  foreignKeys,
  loading,
  onDeleteRows,
  onSelectedRowsChange,
  onSort,
  onUpdateCell,
  page,
  postgresID,
  projectID,
  relations,
  selectedRowKeys = emptySelectedRowKeys,
  selectedTable,
  sort,
  statement,
  tables,
}: {
  columnMeta?: readonly PostgresColumnMeta[];
  enums: ReadonlyMap<number, PostgresEnumType>;
  foreignKeys: PostgresForeignKey[];
  loading: boolean;
  onDeleteRows?: PostgresDeleteRowsHandler;
  onSelectedRowsChange?: (keys: string[]) => void;
  onSort: (column: string, direction?: "asc" | "desc") => void;
  onUpdateCell?: PostgresUpdateCellHandler;
  page: number;
  postgresID: string;
  projectID: string;
  relations: PostgresTableRelation[];
  selectedRowKeys?: readonly string[];
  selectedTable?: PostgresBrowserTable;
  sort?: PostgresTableSort;
  statement?: PostgresStatement;
  tables: PostgresBrowserTable[];
}) => {
  const columns = statement?.columns ?? [];
  const rows = statement?.rows.slice(0, postgresTablePageSize) ?? [];
  const incoming = postgresIncomingRelations(relations);
  const canSelect = Boolean(
    selectedTable &&
    selectedTable.primaryKeyColumns.length > 0 &&
    onDeleteRows &&
    onSelectedRowsChange
  );
  const canEdit = Boolean(
    selectedTable && selectedTable.primaryKeyColumns.length > 0 && onUpdateCell
  );
  const selected = new Set(selectedRowKeys);
  const pageKeys = rows.flatMap((row) => {
    if (!selectedTable) {
      return [];
    }
    const key = rowPrimaryKey(selectedTable, rowColumnValues(columns, row));
    return key ? [key] : [];
  });
  const allPageSelected =
    pageKeys.length > 0 && pageKeys.every((key) => selected.has(key));

  if (!(selectedTable && columns.length > 0)) {
    return <GridEmptyState loading={loading} selectedTable={selectedTable} />;
  }

  const toggleKey = (key: string, checked: boolean) => {
    if (!onSelectedRowsChange) {
      return;
    }
    const next = new Set(selected);
    if (checked) {
      next.add(key);
    } else {
      next.delete(key);
    }
    onSelectedRowsChange([...next]);
  };

  const togglePage = (checked: boolean) => {
    if (!onSelectedRowsChange) {
      return;
    }
    const next = new Set(selected);
    for (const key of pageKeys) {
      if (checked) {
        next.add(key);
      } else {
        next.delete(key);
      }
    }
    onSelectedRowsChange([...next]);
  };

  return (
    <>
      <table className="w-full min-w-max border-collapse text-left text-[10px]">
        <thead className="sticky top-0 z-10 bg-background">
          <tr className="border-b border-border">
            <th className="sticky left-0 z-20 w-14 border-r border-border bg-muted/30 px-2 py-2 text-[8px] font-normal text-muted-foreground">
              {canSelect ? (
                <div className="flex items-center justify-center">
                  <Checkbox
                    aria-label="Select all rows on page"
                    checked={allPageSelected}
                    disabled={pageKeys.length === 0}
                    onCheckedChange={(checked) => togglePage(checked === true)}
                  />
                </div>
              ) : (
                <span className="block text-right">#</span>
              )}
            </th>
            {columns.map((column, columnIndex) => {
              const outgoing = postgresOutgoingRelationForColumn(
                relations,
                column.name
              );
              return (
                <th
                  aria-sort={columnAriaSort(column.name, sort)}
                  className="min-w-40 border-r border-border p-0 last:border-r-0"
                  key={`${columnIndex.toString()}:${column.name}`}
                >
                  <ColumnHeader
                    column={column.name}
                    onSort={onSort}
                    relationLabel={outgoing?.label}
                    sort={sort}
                    typeLabel={postgresTypeLabel(column.typeOid, enums)}
                  />
                </th>
              );
            })}
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
            const rowKey = rowPrimaryKey(selectedTable, columnValues);
            return (
              <tr
                className="border-b border-border last:border-b-0 hover:bg-muted/20"
                key={
                  rowKey ?? (page * postgresTablePageSize + rowIndex).toString()
                }
              >
                <td className="sticky left-0 border-r border-border bg-background px-2 py-2 text-[8px] text-muted-foreground tabular-nums">
                  {canSelect && rowKey ? (
                    <div className="flex items-center justify-center">
                      <Checkbox
                        aria-label={`Select row ${page * postgresTablePageSize + rowIndex + 1}`}
                        checked={selected.has(rowKey)}
                        onCheckedChange={(checked) =>
                          toggleKey(rowKey, checked === true)
                        }
                      />
                    </div>
                  ) : (
                    <span className="block text-right">
                      {page * postgresTablePageSize + rowIndex + 1}
                    </span>
                  )}
                </td>
                {row.map((cell, cellIndex) => {
                  const column = columns[cellIndex];
                  if (!column) {
                    return null;
                  }
                  const editorKind = postgresCellEditorKind(
                    column.typeOid,
                    enums
                  );
                  const meta = columnMeta.find(
                    (candidate) => candidate.name === column.name
                  );
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
                        canEdit={Boolean(canEdit && rowKey)}
                        canSetNull={meta?.nullable ?? true}
                        cell={cell}
                        column={column.name}
                        editorKind={editorKind}
                        enumType={enums.get(column.typeOid)}
                        onSave={
                          onUpdateCell && rowKey
                            ? (value) =>
                                onUpdateCell({
                                  column: column.name,
                                  kind: editorKind,
                                  rowValues: columnValues,
                                  table: selectedTable,
                                  value,
                                })
                            : undefined
                        }
                        relationTrigger={
                          canOpenOutgoing && outgoing ? (
                            <PostgresRelationOutgoingTrigger
                              columnValues={columnValues}
                              enums={enums}
                              foreignKeys={foreignKeys}
                              label={outgoing.label}
                              onDeleteRows={onDeleteRows}
                              onUpdateCell={onUpdateCell}
                              postgresID={postgresID}
                              projectID={projectID}
                              relation={outgoing}
                              tables={tables}
                            />
                          ) : null
                        }
                        typeLabel={postgresTypeLabel(column.typeOid, enums)}
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
                        <PostgresRelationIncomingTrigger
                          columnValues={columnValues}
                          enums={enums}
                          foreignKeys={foreignKeys}
                          label={relation.label}
                          onDeleteRows={onDeleteRows}
                          onUpdateCell={onUpdateCell}
                          postgresID={postgresID}
                          projectID={projectID}
                          relation={relation}
                          tables={tables}
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
