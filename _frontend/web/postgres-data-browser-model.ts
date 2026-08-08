import type { PostgresQueryResult } from "@/api";

export const postgresTablePageSize = 50;
export const postgresPreciseCountThreshold = 100_000;

export interface PostgresBrowserTable {
  approximateRows?: number;
  name: string;
  primaryKeyColumns: string[];
  schema: string;
}

export interface PostgresForeignKey {
  columns: string[];
  foreignColumns: string[];
  foreignSchema: string;
  foreignTable: string;
  name: string;
  schema: string;
  table: string;
}

export interface PostgresEnumType {
  labels: string[];
  name: string;
  typeOID: number;
}

export interface PostgresColumnMeta {
  hasDefault: boolean;
  name: string;
  nullable: boolean;
  typeOID: number;
}

export type PostgresCellEditorKind =
  | "binary"
  | "boolean"
  | "date"
  | "enum"
  | "json"
  | "number"
  | "text"
  | "time"
  | "timestamp";

export type PostgresCellEditValue =
  | { kind: "null" }
  | { kind: "text"; text: string };

export type PostgresRelationDirection = "incoming" | "outgoing";

export interface PostgresTableRelation {
  direction: PostgresRelationDirection;
  foreignKey: PostgresForeignKey;
  key: string;
  label: string;
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

export interface PostgresRelationNavigation {
  filters: PostgresTableFilter[];
  schema: string;
  table: string;
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

export const postgresForeignKeyCatalogSQL = `/* platformd:data-browser:foreign-keys */
SELECT
  source_namespace.nspname AS schema,
  source_relation.relname AS table,
  target_namespace.nspname AS foreign_schema,
  target_relation.relname AS foreign_table,
  constraint_def.conname AS name,
  COALESCE((
    SELECT json_agg(attribute.attname ORDER BY key_column.ordinality)::text
    FROM unnest(constraint_def.conkey)
      WITH ORDINALITY AS key_column(attribute_number, ordinality)
    JOIN pg_attribute AS attribute
      ON attribute.attrelid = constraint_def.conrelid
      AND attribute.attnum = key_column.attribute_number
  ), '[]') AS columns,
  COALESCE((
    SELECT json_agg(attribute.attname ORDER BY key_column.ordinality)::text
    FROM unnest(constraint_def.confkey)
      WITH ORDINALITY AS key_column(attribute_number, ordinality)
    JOIN pg_attribute AS attribute
      ON attribute.attrelid = constraint_def.confrelid
      AND attribute.attnum = key_column.attribute_number
  ), '[]') AS foreign_columns
FROM pg_constraint AS constraint_def
JOIN pg_class AS source_relation ON source_relation.oid = constraint_def.conrelid
JOIN pg_namespace AS source_namespace
  ON source_namespace.oid = source_relation.relnamespace
JOIN pg_class AS target_relation ON target_relation.oid = constraint_def.confrelid
JOIN pg_namespace AS target_namespace
  ON target_namespace.oid = target_relation.relnamespace
WHERE constraint_def.contype = 'f'
  AND source_namespace.nspname NOT IN ('pg_catalog', 'information_schema')
  AND target_namespace.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY
  source_namespace.nspname,
  source_relation.relname,
  constraint_def.conname
LIMIT 2000;`;

export const postgresEnumCatalogSQL = `/* platformd:data-browser:enums */
SELECT
  type_def.oid::text AS type_oid,
  CASE
    WHEN namespace.nspname = 'public' THEN type_def.typname
    ELSE namespace.nspname || '.' || type_def.typname
  END AS type_name,
  COALESCE((
    SELECT json_agg(enum_def.enumlabel ORDER BY enum_def.enumsortorder)::text
    FROM pg_enum AS enum_def
    WHERE enum_def.enumtypid = type_def.oid
  ), '[]') AS labels
FROM pg_type AS type_def
JOIN pg_namespace AS namespace ON namespace.oid = type_def.typnamespace
WHERE type_def.typtype = 'e'
  AND namespace.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY namespace.nspname, type_def.typname
LIMIT 2000;`;

export const quotePostgresIdentifier = (value: string) =>
  `"${value.replaceAll('"', '""')}"`;

export const quotePostgresLiteral = (value: string) =>
  `'${value.replaceAll("'", "''")}'`;

export const postgresColumnMetaSQL = (table: PostgresBrowserTable) =>
  `/* platformd:data-browser:columns */
SELECT
  attribute.attname AS name,
  attribute.atttypid::text AS type_oid,
  CASE WHEN attribute.attnotnull THEN 'false' ELSE 'true' END AS nullable,
  CASE
    WHEN attribute.atthasdef
      OR attribute.attidentity <> ''
      OR attribute.attgenerated <> ''
    THEN 'true'
    ELSE 'false'
  END AS has_default
FROM pg_attribute AS attribute
JOIN pg_class AS relation ON relation.oid = attribute.attrelid
JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = ${quotePostgresLiteral(table.schema)}
  AND relation.relname = ${quotePostgresLiteral(table.name)}
  AND attribute.attnum > 0
  AND NOT attribute.attisdropped
ORDER BY attribute.attnum;`;

export const postgresColumnIsRequired = (column: PostgresColumnMeta) =>
  !(column.nullable || column.hasDefault);

const knownPostgresTypeNames = new Map<number, string>([
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
  [1083, "time"],
  [1114, "timestamp"],
  [1184, "timestamptz"],
  [1266, "timetz"],
  [1700, "numeric"],
  [2950, "uuid"],
  [3802, "jsonb"],
]);

const booleanTypeOIDs = new Set([16]);
const numberTypeOIDs = new Set([20, 21, 23, 700, 701, 1700]);
const dateTypeOIDs = new Set([1082]);
const timeTypeOIDs = new Set([1083, 1266]);
const timestampTypeOIDs = new Set([1114, 1184]);
const jsonTypeOIDs = new Set([114, 3802]);
const binaryTypeOIDs = new Set([17]);

export const postgresTypeLabel = (
  typeOID: number,
  enums: ReadonlyMap<number, PostgresEnumType> = new Map()
) =>
  enums.get(typeOID)?.name ??
  knownPostgresTypeNames.get(typeOID) ??
  `oid ${typeOID.toString()}`;

export const postgresCellEditorKind = (
  typeOID: number,
  enums: ReadonlyMap<number, PostgresEnumType> = new Map()
): PostgresCellEditorKind => {
  if (enums.has(typeOID)) {
    return "enum";
  }
  if (booleanTypeOIDs.has(typeOID)) {
    return "boolean";
  }
  if (numberTypeOIDs.has(typeOID)) {
    return "number";
  }
  if (dateTypeOIDs.has(typeOID)) {
    return "date";
  }
  if (timeTypeOIDs.has(typeOID)) {
    return "time";
  }
  if (timestampTypeOIDs.has(typeOID)) {
    return "timestamp";
  }
  if (jsonTypeOIDs.has(typeOID)) {
    return "json";
  }
  if (binaryTypeOIDs.has(typeOID)) {
    return "binary";
  }
  return "text";
};

const postgresSQLValue = (
  value: PostgresCellEditValue,
  kind: PostgresCellEditorKind
) => {
  if (value.kind === "null") {
    return "NULL";
  }
  const { text } = value;
  if (kind === "boolean") {
    const normalized = text.trim().toLowerCase();
    if (normalized === "true" || normalized === "t" || normalized === "1") {
      return "TRUE";
    }
    if (normalized === "false" || normalized === "f" || normalized === "0") {
      return "FALSE";
    }
  }
  if (
    kind === "number" &&
    /^-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/u.test(text.trim())
  ) {
    return text.trim();
  }
  return quotePostgresLiteral(text);
};

const primaryKeyWhereSQL = (
  table: PostgresBrowserTable,
  rowValues: Record<string, string | undefined>
) => {
  if (table.primaryKeyColumns.length === 0) {
    throw new Error("Table has no primary key");
  }
  const parts = table.primaryKeyColumns.map((column) => {
    const value = rowValues[column];
    if (value === undefined) {
      throw new Error(`Missing primary key value for ${column}`);
    }
    return `${quotePostgresIdentifier(column)} = ${quotePostgresLiteral(value)}`;
  });
  return parts.join(" AND ");
};

export const postgresUpdateCellSQL = ({
  column,
  kind,
  rowValues,
  table,
  value,
}: {
  column: string;
  kind: PostgresCellEditorKind;
  rowValues: Record<string, string | undefined>;
  table: PostgresBrowserTable;
  value: PostgresCellEditValue;
}) => {
  if (kind === "binary") {
    throw new Error("Binary values cannot be edited from the data browser");
  }
  return `/* platformd:data-browser:update */
UPDATE ${quotePostgresIdentifier(table.schema)}.${quotePostgresIdentifier(table.name)}
SET ${quotePostgresIdentifier(column)} = ${postgresSQLValue(value, kind)}
WHERE ${primaryKeyWhereSQL(table, rowValues)};`;
};

export const postgresDeleteRowsSQL = ({
  rows,
  table,
}: {
  rows: Record<string, string | undefined>[];
  table: PostgresBrowserTable;
}) => {
  if (rows.length === 0) {
    throw new Error("No rows selected");
  }
  const predicates = rows.map(
    (rowValues) => `(${primaryKeyWhereSQL(table, rowValues)})`
  );
  return `/* platformd:data-browser:delete */
DELETE FROM ${quotePostgresIdentifier(table.schema)}.${quotePostgresIdentifier(table.name)}
WHERE ${predicates.join("\n   OR ")};`;
};

export const postgresInsertRowSQL = ({
  columns,
  table,
}: {
  columns: {
    column: string;
    kind: PostgresCellEditorKind;
    value: PostgresCellEditValue;
  }[];
  table: PostgresBrowserTable;
}) => {
  const qualified = `${quotePostgresIdentifier(table.schema)}.${quotePostgresIdentifier(table.name)}`;
  if (columns.length === 0) {
    return `/* platformd:data-browser:insert */
INSERT INTO ${qualified}
DEFAULT VALUES;`;
  }
  return `/* platformd:data-browser:insert */
INSERT INTO ${qualified} (
  ${columns.map((item) => quotePostgresIdentifier(item.column)).join(",\n  ")}
)
VALUES (
  ${columns.map((item) => postgresSQLValue(item.value, item.kind)).join(",\n  ")}
);`;
};

export const postgresMutationRowCount = (
  result: PostgresQueryResult,
  command: "DELETE" | "INSERT" | "UPDATE"
) => {
  const tag = result.statements[0]?.commandTag.trim() ?? "";
  if (command === "INSERT") {
    const match = /^INSERT\s+\d+\s+(?<count>\d+)\b/iu.exec(tag);
    return match?.groups?.count === undefined
      ? undefined
      : Number(match.groups.count);
  }
  const match = new RegExp(`^${command}\\s+(?<count>\\d+)\\b`, "iu").exec(tag);
  return match?.groups?.count === undefined
    ? undefined
    : Number(match.groups.count);
};

export const assertPostgresMutationApplied = (
  result: PostgresQueryResult,
  command: "DELETE" | "INSERT" | "UPDATE"
) => {
  const count = postgresMutationRowCount(result, command);
  if (count === undefined) {
    throw new Error(`Unexpected ${command} response`);
  }
  if (count === 0) {
    throw new Error(
      command === "INSERT"
        ? "Insert did not create a row"
        : `No rows were ${command === "UPDATE" ? "updated" : "deleted"}`
    );
  }
};

export const rowPrimaryKey = (
  table: PostgresBrowserTable,
  rowValues: Record<string, string | undefined>
) => {
  if (table.primaryKeyColumns.length === 0) {
    return;
  }
  const parts: string[] = [];
  for (const column of table.primaryKeyColumns) {
    const value = rowValues[column];
    if (value === undefined) {
      return;
    }
    parts.push(`${column}=${value}`);
  }
  return parts.join("\u0000");
};

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

const parseStringArray = (value?: string): string[] => {
  try {
    const parsed = JSON.parse(value ?? "[]");
    if (!Array.isArray(parsed)) {
      return [];
    }
    return parsed.filter((item): item is string => typeof item === "string");
  } catch {
    return [];
  }
};

const tableMatches = (
  schema: string,
  name: string,
  table: PostgresBrowserTable
) => table.schema === schema && table.name === name;

const relationTableLabel = (
  schema: string,
  name: string,
  currentSchema: string
) => (schema === currentSchema ? name : `${schema}.${name}`);

export const postgresForeignKeysFromResult = (
  result: PostgresQueryResult
): PostgresForeignKey[] => {
  const [statement] = result.statements;
  if (!statement) {
    return [];
  }
  const indexes = new Map(
    statement.columns.map((column, index) => [column.name, index])
  );
  const schemaIndex = indexes.get("schema");
  const tableIndex = indexes.get("table");
  const foreignSchemaIndex = indexes.get("foreign_schema");
  const foreignTableIndex = indexes.get("foreign_table");
  const nameIndex = indexes.get("name");
  const columnsIndex = indexes.get("columns");
  const foreignColumnsIndex = indexes.get("foreign_columns");
  if (
    schemaIndex === undefined ||
    tableIndex === undefined ||
    foreignSchemaIndex === undefined ||
    foreignTableIndex === undefined
  ) {
    return [];
  }
  return statement.rows.flatMap((row) => {
    const schema = row[schemaIndex]?.text;
    const table = row[tableIndex]?.text;
    const foreignSchema = row[foreignSchemaIndex]?.text;
    const foreignTable = row[foreignTableIndex]?.text;
    if (!(schema && table && foreignSchema && foreignTable)) {
      return [];
    }
    const columns = parseStringArray(row[columnsIndex ?? -1]?.text);
    const foreignColumns = parseStringArray(
      row[foreignColumnsIndex ?? -1]?.text
    );
    if (columns.length === 0 || columns.length !== foreignColumns.length) {
      return [];
    }
    return [
      {
        columns,
        foreignColumns,
        foreignSchema,
        foreignTable,
        name:
          row[nameIndex ?? -1]?.text ?? `${table}_${columns.join("_")}_fkey`,
        schema,
        table,
      },
    ];
  });
};

export const postgresEnumsFromResult = (
  result: PostgresQueryResult
): Map<number, PostgresEnumType> => {
  const enums = new Map<number, PostgresEnumType>();
  const [statement] = result.statements;
  if (!statement) {
    return enums;
  }
  const indexes = new Map(
    statement.columns.map((column, index) => [column.name, index])
  );
  const typeOIDIndex = indexes.get("type_oid");
  const typeNameIndex = indexes.get("type_name");
  const labelsIndex = indexes.get("labels");
  if (typeOIDIndex === undefined || typeNameIndex === undefined) {
    return enums;
  }
  for (const row of statement.rows) {
    const typeOID = Number(row[typeOIDIndex]?.text);
    const name = row[typeNameIndex]?.text;
    if (!(Number.isSafeInteger(typeOID) && typeOID > 0 && name)) {
      continue;
    }
    const labels = parseStringArray(row[labelsIndex ?? -1]?.text);
    if (labels.length === 0) {
      continue;
    }
    enums.set(typeOID, { labels, name, typeOID });
  }
  return enums;
};

export const postgresColumnsFromResult = (
  result: PostgresQueryResult
): PostgresColumnMeta[] => {
  const [statement] = result.statements;
  if (!statement) {
    return [];
  }
  const indexes = new Map(
    statement.columns.map((column, index) => [column.name, index])
  );
  const nameIndex = indexes.get("name");
  const typeOIDIndex = indexes.get("type_oid");
  const nullableIndex = indexes.get("nullable");
  const hasDefaultIndex = indexes.get("has_default");
  if (nameIndex === undefined || typeOIDIndex === undefined) {
    return [];
  }
  return statement.rows.flatMap((row) => {
    const name = row[nameIndex]?.text;
    const typeOID = Number(row[typeOIDIndex]?.text);
    if (!(name && Number.isSafeInteger(typeOID) && typeOID > 0)) {
      return [];
    }
    return [
      {
        hasDefault: row[hasDefaultIndex ?? -1]?.text === "true",
        name,
        nullable: row[nullableIndex ?? -1]?.text !== "false",
        typeOID,
      },
    ];
  });
};

export const postgresRelationsForTable = (
  foreignKeys: PostgresForeignKey[],
  table: PostgresBrowserTable
): PostgresTableRelation[] => {
  const outgoing = foreignKeys
    .filter((foreignKey) =>
      tableMatches(foreignKey.schema, foreignKey.table, table)
    )
    .map((foreignKey) => ({
      direction: "outgoing" as const,
      foreignKey,
      key: `out:${foreignKey.name}`,
      label: relationTableLabel(
        foreignKey.foreignSchema,
        foreignKey.foreignTable,
        table.schema
      ),
    }));
  const incomingLabelCounts = new Map<string, number>();
  for (const foreignKey of foreignKeys) {
    if (
      !tableMatches(foreignKey.foreignSchema, foreignKey.foreignTable, table)
    ) {
      continue;
    }
    const label = relationTableLabel(
      foreignKey.schema,
      foreignKey.table,
      table.schema
    );
    incomingLabelCounts.set(label, (incomingLabelCounts.get(label) ?? 0) + 1);
  }
  const incoming = foreignKeys
    .filter((foreignKey) =>
      tableMatches(foreignKey.foreignSchema, foreignKey.foreignTable, table)
    )
    .map((foreignKey) => {
      const baseLabel = relationTableLabel(
        foreignKey.schema,
        foreignKey.table,
        table.schema
      );
      const needsDisambiguation = (incomingLabelCounts.get(baseLabel) ?? 0) > 1;
      return {
        direction: "incoming" as const,
        foreignKey,
        key: `in:${foreignKey.name}`,
        label: needsDisambiguation
          ? `${baseLabel} (${foreignKey.columns.join(", ")})`
          : baseLabel,
      };
    });
  return [...outgoing, ...incoming];
};

export const postgresOutgoingRelationForColumn = (
  relations: PostgresTableRelation[],
  column: string
): PostgresTableRelation | undefined =>
  relations.find(
    (relation) =>
      relation.direction === "outgoing" &&
      relation.foreignKey.columns.includes(column)
  );

export const postgresIncomingRelations = (
  relations: PostgresTableRelation[]
): PostgresTableRelation[] =>
  relations.filter((relation) => relation.direction === "incoming");

export const postgresRelationNavigation = ({
  columnValues,
  relation,
}: {
  columnValues: Record<string, string | undefined>;
  relation: PostgresTableRelation;
}): PostgresRelationNavigation | undefined => {
  const { foreignKey } = relation;
  const sourceColumns =
    relation.direction === "outgoing"
      ? foreignKey.columns
      : foreignKey.foreignColumns;
  const targetColumns =
    relation.direction === "outgoing"
      ? foreignKey.foreignColumns
      : foreignKey.columns;
  const filters: PostgresTableFilter[] = [];
  for (const [index, sourceColumn] of sourceColumns.entries()) {
    const targetColumn = targetColumns[index];
    const value = columnValues[sourceColumn];
    if (!(targetColumn && value !== undefined)) {
      return undefined;
    }
    filters.push({
      column: targetColumn,
      connector: "and",
      id: `relation:${sourceColumn}:${targetColumn}`,
      operator: "=",
      value,
    });
  }
  if (filters.length === 0) {
    return undefined;
  }
  return {
    filters,
    schema:
      relation.direction === "outgoing"
        ? foreignKey.foreignSchema
        : foreignKey.schema,
    table:
      relation.direction === "outgoing"
        ? foreignKey.foreignTable
        : foreignKey.table,
  };
};
