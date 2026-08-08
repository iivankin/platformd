import { useCallback, useEffect, useMemo, useState } from "react";

import { queryManagedPostgres } from "@/api";
import type { PostgresQueryResult } from "@/api";
import type { PostgresInsertRowHandler } from "@/postgres-data-add-record";
import {
  activePostgresFilters,
  assertPostgresMutationApplied,
  postgresColumnMetaSQL,
  postgresColumnsFromResult,
  postgresCountFromResult,
  postgresDeleteRowsSQL,
  postgresEnumCatalogSQL,
  postgresEnumsFromResult,
  postgresForeignKeyCatalogSQL,
  postgresForeignKeysFromResult,
  postgresInsertRowSQL,
  postgresPreciseCountThreshold,
  postgresRelationsForTable,
  postgresTableCatalogSQL,
  postgresTableCountSQL,
  postgresTableDataSQL,
  postgresTableSelectSQL,
  postgresTablesFromResult,
  postgresUpdateCellSQL,
} from "@/postgres-data-browser-model";
import type {
  PostgresBrowserTable,
  PostgresColumnMeta,
  PostgresEnumType,
  PostgresForeignKey,
  PostgresTableFilter,
  PostgresTableSort,
} from "@/postgres-data-browser-model";
import { PostgresDataBrowserSidebar } from "@/postgres-data-browser-sidebar";
import { PostgresDataFilters } from "@/postgres-data-filters";
import { PostgresDataGrid } from "@/postgres-data-grid";
import type {
  PostgresDeleteRowsHandler,
  PostgresUpdateCellHandler,
} from "@/postgres-data-grid-table";

type PostgresStatement = PostgresQueryResult["statements"][number];

const tableIdentity = (table: PostgresBrowserTable) =>
  `${table.schema}\u0000${table.name}`;
const noPostgresFilters: PostgresTableFilter[] = [];

const errorMessage = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const useDebouncedPostgresFilters = (
  filters: PostgresTableFilter[],
  tableID: string
) => {
  const filtersKey = JSON.stringify(filters);
  const [state, setState] = useState<{
    filters: PostgresTableFilter[];
    key: string;
    tableID: string;
  }>({ filters: noPostgresFilters, key: "[]", tableID: "" });

  useEffect(() => {
    const timeout = setTimeout(() => {
      setState((current) => {
        if (current.key === filtersKey && current.tableID === tableID) {
          return current;
        }
        return {
          filters:
            filtersKey === "[]"
              ? noPostgresFilters
              : (JSON.parse(filtersKey) as PostgresTableFilter[]),
          key: filtersKey,
          tableID,
        };
      });
    }, 250);
    return () => clearTimeout(timeout);
  }, [filtersKey, tableID]);

  return state.tableID === tableID ? state.filters : noPostgresFilters;
};

const usePostgresCatalog = (projectID: string, postgresID: string) => {
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [tables, setTables] = useState<PostgresBrowserTable[]>([]);
  const [foreignKeys, setForeignKeys] = useState<PostgresForeignKey[]>([]);
  const [enums, setEnums] = useState<Map<number, PostgresEnumType>>(
    () => new Map()
  );
  const [selectedTable, setSelectedTable] = useState<PostgresBrowserTable>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      setLoading(true);
      try {
        const [tablesSettled, foreignKeysSettled, enumsSettled] =
          await Promise.all([
            queryManagedPostgres(
              projectID,
              postgresID,
              postgresTableCatalogSQL,
              controller.signal
            ),
            queryManagedPostgres(
              projectID,
              postgresID,
              postgresForeignKeyCatalogSQL,
              controller.signal
            ).then(
              (result) => ({ ok: true as const, result }),
              (foreignKeyError: unknown) => {
                if (
                  foreignKeyError instanceof Error &&
                  foreignKeyError.name === "AbortError"
                ) {
                  throw foreignKeyError;
                }
                return { error: foreignKeyError, ok: false as const };
              }
            ),
            queryManagedPostgres(
              projectID,
              postgresID,
              postgresEnumCatalogSQL,
              controller.signal
            ).then(
              (result) => ({ ok: true as const, result }),
              (enumError: unknown) => {
                if (
                  enumError instanceof Error &&
                  enumError.name === "AbortError"
                ) {
                  throw enumError;
                }
                return { error: enumError, ok: false as const };
              }
            ),
          ]);
        if (controller.signal.aborted) {
          return;
        }
        const loadedTables = postgresTablesFromResult(tablesSettled);
        const loadedForeignKeys = foreignKeysSettled.ok
          ? postgresForeignKeysFromResult(foreignKeysSettled.result)
          : [];
        const loadedEnums = enumsSettled.ok
          ? postgresEnumsFromResult(enumsSettled.result)
          : new Map<number, PostgresEnumType>();
        const schemas = [...new Set(loadedTables.map((table) => table.schema))];
        const defaultSchema = schemas.includes("public")
          ? "public"
          : (schemas[0] ?? "");
        setTables(loadedTables);
        setForeignKeys(loadedForeignKeys);
        setEnums(loadedEnums);
        setSelectedTable((current) => {
          const currentMatch = loadedTables.find(
            (table) =>
              current && tableIdentity(table) === tableIdentity(current)
          );
          return (
            currentMatch ??
            loadedTables.find(
              (table) => table.schema === (current?.schema ?? defaultSchema)
            ) ??
            loadedTables.find((table) => table.schema === defaultSchema)
          );
        });
        if (foreignKeysSettled.ok) {
          if (enumsSettled.ok) {
            setError(undefined);
          } else {
            setError(
              errorMessage(
                enumsSettled.error,
                "Unable to load PostgreSQL enums"
              )
            );
          }
        } else {
          setError(
            errorMessage(
              foreignKeysSettled.error,
              "Unable to load PostgreSQL foreign keys"
            )
          );
        }
      } catch (loadError) {
        if (loadError instanceof Error && loadError.name === "AbortError") {
          return;
        }
        setError(errorMessage(loadError, "Unable to load PostgreSQL tables"));
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [postgresID, projectID, refreshVersion]);

  return {
    enums,
    error,
    foreignKeys,
    handleRefresh: () => setRefreshVersion((value) => value + 1),
    loading,
    selectedSchema: selectedTable?.schema ?? "",
    selectedTable,
    setSelectedTable,
    tables,
  };
};

const usePostgresColumnMeta = ({
  postgresID,
  projectID,
  selectedTable,
}: {
  postgresID: string;
  projectID: string;
  selectedTable?: PostgresBrowserTable;
}) => {
  const selectedTableID = selectedTable ? tableIdentity(selectedTable) : "";
  const [state, setState] = useState<{
    columns: PostgresColumnMeta[];
    tableID: string;
  }>({ columns: [], tableID: "" });

  useEffect(() => {
    if (!selectedTable) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const result = await queryManagedPostgres(
          projectID,
          postgresID,
          postgresColumnMetaSQL(selectedTable),
          controller.signal
        );
        if (controller.signal.aborted) {
          return;
        }
        setState({
          columns: postgresColumnsFromResult(result),
          tableID: selectedTableID,
        });
      } catch (loadError) {
        if (loadError instanceof Error && loadError.name === "AbortError") {
          return;
        }
        setState({ columns: [], tableID: selectedTableID });
      }
    };
    void load();
    return () => controller.abort();
  }, [postgresID, projectID, selectedTable, selectedTableID]);

  return state.tableID === selectedTableID ? state.columns : [];
};

const usePostgresTable = ({
  filters,
  page,
  postgresID,
  projectID,
  selectedTable,
  sort,
}: {
  filters: PostgresTableFilter[];
  page: number;
  postgresID: string;
  projectID: string;
  selectedTable?: PostgresBrowserTable;
  sort?: PostgresTableSort;
}) => {
  const [refreshVersion, setRefreshVersion] = useState(0);
  const selectedTableID = selectedTable ? tableIdentity(selectedTable) : "";
  const requestKey = JSON.stringify([
    projectID,
    postgresID,
    selectedTableID,
    filters,
    page,
    sort,
    refreshVersion,
  ]);
  const [loadState, setLoadState] = useState<{
    columns: PostgresStatement["columns"];
    error?: string;
    key: string;
    loading: boolean;
    statement?: PostgresStatement;
    tableID: string;
  }>({ columns: [], key: "", loading: false, tableID: "" });

  useEffect(() => {
    if (!selectedTable) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      setLoadState((current) => ({
        columns: current.tableID === selectedTableID ? current.columns : [],
        key: requestKey,
        loading: true,
        tableID: selectedTableID,
      }));
      try {
        const result = await queryManagedPostgres(
          projectID,
          postgresID,
          postgresTableDataSQL({ filters, page, sort, table: selectedTable }),
          controller.signal
        );
        if (controller.signal.aborted) {
          return;
        }
        const [loadedStatement] = result.statements;
        setLoadState({
          columns: loadedStatement?.columns ?? [],
          key: requestKey,
          loading: false,
          statement: loadedStatement,
          tableID: selectedTableID,
        });
      } catch (loadError) {
        if (loadError instanceof Error && loadError.name === "AbortError") {
          return;
        }
        setLoadState((current) => ({
          ...current,
          error: errorMessage(loadError, "Unable to load PostgreSQL rows"),
          key: requestKey,
          loading: false,
          statement: undefined,
          tableID: selectedTableID,
        }));
      }
    };
    void load();
    return () => controller.abort();
  }, [
    filters,
    page,
    postgresID,
    projectID,
    refreshVersion,
    requestKey,
    selectedTable,
    selectedTableID,
    sort,
  ]);

  const currentState = loadState.key === requestKey ? loadState : undefined;
  const currentColumns =
    loadState.tableID === selectedTableID ? loadState.columns : [];

  return {
    columns: currentColumns,
    error: currentState?.error,
    handleRefresh: () => setRefreshVersion((value) => value + 1),
    loading: Boolean(selectedTable && (!currentState || currentState.loading)),
    statement: currentState?.statement,
  };
};

const usePostgresTableCount = ({
  filters,
  postgresID,
  projectID,
  selectedTable,
}: {
  filters: PostgresTableFilter[];
  postgresID: string;
  projectID: string;
  selectedTable?: PostgresBrowserTable;
}) => {
  const [forcedTableID, setForcedTableID] = useState("");
  const [refreshVersion, setRefreshVersion] = useState(0);
  const selectedTableID = selectedTable ? tableIdentity(selectedTable) : "";
  const countKey = JSON.stringify([
    projectID,
    postgresID,
    selectedTableID,
    filters,
    refreshVersion,
  ]);
  const [countState, setCountState] = useState<{
    count?: number;
    key: string;
  }>({ key: "" });
  const shouldLoadExact = Boolean(
    selectedTable &&
    (selectedTable.approximateRows === undefined ||
      selectedTable.approximateRows < postgresPreciseCountThreshold ||
      forcedTableID === selectedTableID)
  );

  useEffect(() => {
    if (!(selectedTable && shouldLoadExact)) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const result = await queryManagedPostgres(
          projectID,
          postgresID,
          postgresTableCountSQL({ filters, table: selectedTable }),
          controller.signal
        );
        if (controller.signal.aborted) {
          return;
        }
        setCountState({
          count: postgresCountFromResult(result),
          key: countKey,
        });
      } catch (loadError) {
        if (!(loadError instanceof Error && loadError.name === "AbortError")) {
          setCountState({ key: countKey });
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [
    countKey,
    filters,
    postgresID,
    projectID,
    refreshVersion,
    selectedTable,
    selectedTableID,
    shouldLoadExact,
  ]);

  const exactCount = countState.key === countKey ? countState.count : undefined;

  return {
    approximate: exactCount === undefined,
    count:
      exactCount ??
      (filters.length === 0 ? selectedTable?.approximateRows : undefined),
    handleRefresh: () => setRefreshVersion((value) => value + 1),
    handleRequestExact: () => {
      setForcedTableID(selectedTableID);
      setRefreshVersion((value) => value + 1);
    },
  };
};

const newFilter = (column: string): PostgresTableFilter => ({
  column,
  connector: "and",
  id: globalThis.crypto.randomUUID(),
  operator: "=",
  value: "",
});

export const PostgresDataBrowser = ({
  onOpenInQuery,
  postgresID,
  projectID,
}: {
  onOpenInQuery?: (sql: string) => void;
  postgresID: string;
  projectID: string;
}) => {
  const catalog = usePostgresCatalog(projectID, postgresID);
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(0);
  const [sort, setSort] = useState<PostgresTableSort>();
  const [filters, setFilters] = useState<PostgresTableFilter[]>([]);
  const [filtersVisible, setFiltersVisible] = useState(false);
  const activeFilters = useMemo(
    () => activePostgresFilters(filters),
    [filters]
  );
  const selectedTableID = catalog.selectedTable
    ? tableIdentity(catalog.selectedTable)
    : "";
  const appliedFilters = useDebouncedPostgresFilters(
    activeFilters,
    selectedTableID
  );
  const tableData = usePostgresTable({
    filters: appliedFilters,
    page,
    postgresID,
    projectID,
    selectedTable: catalog.selectedTable,
    sort,
  });
  const tableCount = usePostgresTableCount({
    filters: appliedFilters,
    postgresID,
    projectID,
    selectedTable: catalog.selectedTable,
  });
  const columnMeta = usePostgresColumnMeta({
    postgresID,
    projectID,
    selectedTable: catalog.selectedTable,
  });

  const schemas = useMemo(
    () => [...new Set(catalog.tables.map((table) => table.schema))],
    [catalog.tables]
  );
  const visibleTables = useMemo(() => {
    const normalizedSearch = search.trim().toLowerCase();
    return catalog.tables.filter(
      (table) =>
        table.schema === catalog.selectedSchema &&
        (!normalizedSearch ||
          table.name.toLowerCase().includes(normalizedSearch))
    );
  }, [catalog.selectedSchema, catalog.tables, search]);
  const relations = useMemo(
    () =>
      catalog.selectedTable
        ? postgresRelationsForTable(catalog.foreignKeys, catalog.selectedTable)
        : [],
    [catalog.foreignKeys, catalog.selectedTable]
  );

  const resetTableView = useCallback(() => {
    setFilters([]);
    setPage(0);
    setSort(undefined);
  }, []);
  const selectSchema = (schema: string) => {
    catalog.setSelectedTable(
      catalog.tables.find((table) => table.schema === schema)
    );
    resetTableView();
  };
  const selectTable = (table: PostgresBrowserTable) => {
    catalog.setSelectedTable(table);
    resetTableView();
  };
  const changeSort = (column: string, direction?: "asc" | "desc") => {
    setSort(direction ? { column, direction } : undefined);
    setPage(0);
  };
  const updateFilter = (nextFilter: PostgresTableFilter) => {
    setFilters((current) =>
      current.map((filter) =>
        filter.id === nextFilter.id ? nextFilter : filter
      )
    );
    setPage(0);
  };
  const handleRefresh = () => {
    tableData.handleRefresh();
    tableCount.handleRefresh();
  };
  const updateCell: PostgresUpdateCellHandler = async (input) => {
    const result = await queryManagedPostgres(
      projectID,
      postgresID,
      postgresUpdateCellSQL(input)
    );
    assertPostgresMutationApplied(result, "UPDATE");
    handleRefresh();
  };
  const deleteRows: PostgresDeleteRowsHandler = async (input) => {
    const result = await queryManagedPostgres(
      projectID,
      postgresID,
      postgresDeleteRowsSQL(input)
    );
    assertPostgresMutationApplied(result, "DELETE");
    handleRefresh();
  };
  const insertRow: PostgresInsertRowHandler = async (input) => {
    const result = await queryManagedPostgres(
      projectID,
      postgresID,
      postgresInsertRowSQL(input)
    );
    assertPostgresMutationApplied(result, "INSERT");
    handleRefresh();
  };
  const columnNames = tableData.columns.map((column) => column.name);
  const { selectedTable } = catalog;
  const openInQuery =
    onOpenInQuery && selectedTable
      ? () =>
          onOpenInQuery(
            `${postgresTableSelectSQL({
              filters: activeFilters,
              sort,
              table: selectedTable,
            })};`
          )
      : undefined;

  return (
    <div className="grid min-h-[36rem] grid-cols-1 overflow-hidden border border-border lg:grid-cols-[13rem_minmax(0,1fr)]">
      <PostgresDataBrowserSidebar
        loading={catalog.loading}
        onRefresh={catalog.handleRefresh}
        onSchemaChange={selectSchema}
        onSearchChange={setSearch}
        onTableChange={selectTable}
        schemas={schemas}
        search={search}
        selectedSchema={catalog.selectedSchema}
        selectedTable={catalog.selectedTable}
        tableCount={catalog.tables.length}
        tables={visibleTables}
      />
      <PostgresDataGrid
        approximateCount={tableCount.approximate}
        columnMeta={columnMeta}
        count={tableCount.count}
        enums={catalog.enums}
        error={catalog.error ?? tableData.error}
        filterPanel={
          filtersVisible ? (
            <PostgresDataFilters
              columns={columnNames}
              filters={filters}
              onAdd={() => {
                const [column] = columnNames;
                if (column) {
                  setFilters((current) => [...current, newFilter(column)]);
                }
              }}
              onChange={updateFilter}
              onClear={() => {
                setFilters([]);
                setPage(0);
              }}
              onClose={() => setFiltersVisible(false)}
              onOpenInQuery={openInQuery}
              onRemove={(id) => {
                setFilters((current) =>
                  current.filter((filter) => filter.id !== id)
                );
                setPage(0);
              }}
            />
          ) : null
        }
        filtersActive={activeFilters.length > 0}
        foreignKeys={catalog.foreignKeys}
        loading={tableData.loading}
        onDeleteRows={deleteRows}
        onInsertRow={insertRow}
        onNextPage={() => setPage((value) => value + 1)}
        onPreviousPage={() => setPage((value) => Math.max(0, value - 1))}
        onRefresh={handleRefresh}
        onRequestExactCount={tableCount.handleRequestExact}
        onSort={changeSort}
        onToggleFilters={() => setFiltersVisible((visible) => !visible)}
        onUpdateCell={updateCell}
        page={page}
        postgresID={postgresID}
        projectID={projectID}
        relations={relations}
        selectedTable={catalog.selectedTable}
        sort={sort}
        statement={tableData.statement}
        tables={catalog.tables}
      />
    </div>
  );
};
