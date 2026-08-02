import type { PostgresQueryResult } from "@/api";

export const postgresTablePageSize = 50;
export const postgresPreciseCountThreshold = 100_000;

export interface PostgresBrowserTable {
  approximateRows?: number;
  name: string;
  primaryKeyColumns: string[];
  schema: string;
}

export interface PostgresTableSort {
  column: string;
  direction: "asc" | "desc";
}

export type PostgresFilterConnector = "and" | "or";
export type PostgresFilterOperator =
  | "="
  | "<>"
  | ">"
  | ">="
  | "<"
  | "<="
  | "like"
  | "ilike"
  | "not like"
  | "in"
  | "is null"
  | "is not null";

export interface PostgresTableFilter {
  column: string;
  connector: PostgresFilterConnector;
  id: string;
  operator: PostgresFilterOperator;
  value: string;
}

export const postgresFilterOperators: {
  label: string;
  value: PostgresFilterOperator;
}[] = [
  { label: "equals", value: "=" },
  { label: "not equal", value: "<>" },
  { label: "greater than", value: ">" },
  { label: "greater or equal", value: ">=" },
  { label: "less than", value: "<" },
  { label: "less or equal", value: "<=" },
  { label: "like", value: "like" },
  { label: "ilike", value: "ilike" },
  { label: "not like", value: "not like" },
  { label: "in", value: "in" },
  { label: "is null", value: "is null" },
  { label: "is not null", value: "is not null" },
];

export const postgresTableCatalogSQL = `/* platformd:data-browser:catalog */
SELECT
  namespace.nspname AS schema,
  relation.relname AS table,
  GREATEST(
    COALESCE(statistics.n_live_tup, 0),
    COALESCE(relation.reltuples, 0)
  )::bigint::text AS approximate_rows,
  COALESCE((
    SELECT json_agg(attribute.attname ORDER BY key_column.ordinality)::text
    FROM pg_index AS primary_index
    CROSS JOIN LATERAL unnest(primary_index.indkey)
      WITH ORDINALITY AS key_column(attribute_number, ordinality)
    JOIN pg_attribute AS attribute
      ON attribute.attrelid = primary_index.indrelid
      AND attribute.attnum = key_column.attribute_number
    WHERE primary_index.indrelid = relation.oid
      AND primary_index.indisprimary
  ), '[]') AS primary_key_columns
FROM pg_class AS relation
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
LEFT JOIN pg_stat_user_tables AS statistics
  ON statistics.relid = relation.oid
WHERE relation.relkind IN ('r', 'p')
  AND namespace.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY namespace.nspname, relation.relname
LIMIT 500;`;

export const quotePostgresIdentifier = (value: string) =>
  `"${value.replaceAll('"', '""')}"`;

export const quotePostgresLiteral = (value: string) =>
  `'${value.replaceAll("'", "''")}'`;

const filterNeedsValue = (operator: PostgresFilterOperator) =>
  operator !== "is null" && operator !== "is not null";

const filterHasValue = (filter: PostgresTableFilter) => {
  if (!filterNeedsValue(filter.operator)) {
    return true;
  }
  if (filter.operator === "in") {
    return filter.value.split(",").some((value) => value.trim().length > 0);
  }
  return filter.value.trim().length > 0;
};

export const activePostgresFilters = (filters: PostgresTableFilter[]) =>
  filters.filter((filter) => filter.column && filterHasValue(filter));

const filterConditionSQL = (filter: PostgresTableFilter) => {
  const column = quotePostgresIdentifier(filter.column);
  const operator = filter.operator.toUpperCase();
  if (!filterNeedsValue(filter.operator)) {
    return `${column} ${operator}`;
  }
  if (filter.operator === "in") {
    const values = filter.value
      .split(",")
      .map((value) => value.trim())
      .filter(Boolean)
      .map(quotePostgresLiteral);
    return `${column} IN (${values.join(", ")})`;
  }
  return `${column} ${operator} ${quotePostgresLiteral(filter.value)}`;
};

const postgresWhereSQL = (filters: PostgresTableFilter[] = []) => {
  const activeFilters = activePostgresFilters(filters);
  if (activeFilters.length === 0) {
    return "";
  }
  return `\nWHERE ${activeFilters
    .map((filter, index) => {
      const connector = index === 0 ? "" : `${filter.connector.toUpperCase()} `;
      return `${connector}${filterConditionSQL(filter)}`;
    })
    .join("\n  ")}`;
};

const postgresOrderSQL = (
  table: PostgresBrowserTable,
  sort?: PostgresTableSort
) => {
  const order = sort
    ? [sort]
    : table.primaryKeyColumns.map((column) => ({
        column,
        direction: "asc" as const,
      }));
  if (order.length === 0) {
    return "";
  }
  return `\nORDER BY ${order
    .map(
      (item) =>
        `${quotePostgresIdentifier(item.column)} ${item.direction.toUpperCase()}`
    )
    .join(", ")}`;
};

export const postgresTableSelectSQL = ({
  filters = [],
  sort,
  table,
}: {
  filters?: PostgresTableFilter[];
  sort?: PostgresTableSort;
  table: PostgresBrowserTable;
}) => `SELECT *
FROM ${quotePostgresIdentifier(table.schema)}.${quotePostgresIdentifier(table.name)}${postgresWhereSQL(filters)}${postgresOrderSQL(table, sort)}`;

export const postgresTableDataSQL = ({
  filters = [],
  page,
  sort,
  table,
}: {
  filters?: PostgresTableFilter[];
  page: number;
  sort?: PostgresTableSort;
  table: PostgresBrowserTable;
}) => {
  const safePage = Math.max(0, Math.trunc(page));
  return `/* platformd:data-browser:table */
${postgresTableSelectSQL({ filters, sort, table })}
LIMIT ${postgresTablePageSize + 1}
OFFSET ${safePage * postgresTablePageSize};`;
};

export const postgresTableCountSQL = ({
  filters = [],
  table,
}: {
  filters?: PostgresTableFilter[];
  table: PostgresBrowserTable;
}) => `/* platformd:data-browser:count */
SELECT COUNT(*)::bigint::text AS count
FROM ${quotePostgresIdentifier(table.schema)}.${quotePostgresIdentifier(table.name)}${postgresWhereSQL(filters)};`;

export const postgresCountFromResult = (
  result: PostgresQueryResult
): number | undefined => {
  const [statement] = result.statements;
  const countIndex = statement?.columns.findIndex(
    (column) => column.name === "count"
  );
  if (!(statement && countIndex !== undefined && countIndex >= 0)) {
    return undefined;
  }
  const count = Number(statement.rows[0]?.[countIndex]?.text);
  return Number.isSafeInteger(count) && count >= 0 ? count : undefined;
};

export const postgresTablesFromResult = (
  result: PostgresQueryResult
): PostgresBrowserTable[] => {
  const [statement] = result.statements;
  if (!statement) {
    return [];
  }
  const indexes = new Map(
    statement.columns.map((column, index) => [column.name, index])
  );
  const schemaIndex = indexes.get("schema");
  const tableIndex = indexes.get("table");
  const rowsIndex = indexes.get("approximate_rows");
  const primaryKeyIndex = indexes.get("primary_key_columns");
  if (schemaIndex === undefined || tableIndex === undefined) {
    return [];
  }
  return statement.rows.flatMap((row) => {
    const schema = row[schemaIndex]?.text;
    const name = row[tableIndex]?.text;
    if (!(schema && name)) {
      return [];
    }
    const approximateRows = Math.trunc(
      Number(row[rowsIndex ?? -1]?.text ?? "")
    );
    let primaryKeyColumns: string[] = [];
    try {
      const parsed = JSON.parse(row[primaryKeyIndex ?? -1]?.text ?? "[]");
      if (Array.isArray(parsed)) {
        primaryKeyColumns = parsed.filter(
          (column): column is string => typeof column === "string"
        );
      }
    } catch {
      primaryKeyColumns = [];
    }
    return [
      {
        approximateRows: Number.isFinite(approximateRows)
          ? Math.max(0, approximateRows)
          : undefined,
        name,
        primaryKeyColumns,
        schema,
      },
    ];
  });
};
