import type { PostgresQueryResult } from "@/api";

export interface PostgresQueryCatalogColumn {
  column: string;
  schema: string;
  table: string;
}

type PostgresQuerySuggestionKind = "column" | "keyword" | "schema" | "table";

export interface PostgresQuerySuggestion {
  detail: string;
  insertText: string;
  key: string;
  kind: PostgresQuerySuggestionKind;
  label: string;
}

interface PostgresQueryCompletion {
  from: number;
  items: PostgresQuerySuggestion[];
  to: number;
}

export const postgresStarterSQL = `SELECT
  schemaname AS schema,
  relname AS table,
  n_live_tup AS approximate_rows
FROM pg_stat_user_tables
ORDER BY schemaname, relname
LIMIT 100;`;

export const postgresQueryCatalogSQL = `/* platformd:query-autocomplete */
SELECT
  columns.table_schema AS schema,
  columns.table_name AS table,
  columns.column_name AS column
FROM information_schema.columns AS columns
JOIN information_schema.tables AS tables
  ON tables.table_schema = columns.table_schema
  AND tables.table_name = columns.table_name
WHERE columns.table_schema NOT IN ('pg_catalog', 'information_schema')
  AND tables.table_type IN ('BASE TABLE', 'VIEW', 'FOREIGN')
ORDER BY columns.table_schema, columns.table_name, columns.ordinal_position
LIMIT 1000;`;

const postgresKeywords = [
  "SELECT",
  "FROM",
  "WHERE",
  "JOIN",
  "LEFT JOIN",
  "RIGHT JOIN",
  "FULL JOIN",
  "INNER JOIN",
  "ON",
  "AS",
  "AND",
  "OR",
  "NOT",
  "NULL",
  "IS NULL",
  "IS NOT NULL",
  "IN",
  "EXISTS",
  "LIKE",
  "ILIKE",
  "BETWEEN",
  "GROUP BY",
  "HAVING",
  "ORDER BY",
  "ASC",
  "DESC",
  "LIMIT",
  "OFFSET",
  "DISTINCT",
  "WITH",
  "UNION",
  "UNION ALL",
  "INSERT INTO",
  "VALUES",
  "UPDATE",
  "SET",
  "DELETE FROM",
  "RETURNING",
  "CREATE TABLE",
  "ALTER TABLE",
  "DROP TABLE",
  "BEGIN",
  "COMMIT",
  "ROLLBACK",
] as const;

const completionTokenCharacter = /[\w$".]/u;
const safeUnquotedIdentifier = /^[a-z_][a-z0-9_$]*$/u;

const insertionIdentifier = (value: string) =>
  safeUnquotedIdentifier.test(value)
    ? value
    : `"${value.replaceAll('"', '""')}"`;

const normalizedIdentifier = (value: string) =>
  value.replaceAll(/^"|"$/gu, "").toLocaleLowerCase();

const keywordSuggestions = (): PostgresQuerySuggestion[] =>
  postgresKeywords.map((keyword) => ({
    detail: "keyword",
    insertText: keyword,
    key: `keyword:${keyword}`,
    kind: "keyword",
    label: keyword,
  }));

const catalogSuggestions = (
  catalog: PostgresQueryCatalogColumn[],
  qualifier?: string,
  schemaQualifier?: string
) => {
  const suggestions: PostgresQuerySuggestion[] = [];
  const schemas = new Set<string>();
  const tables = new Set<string>();
  const columns = new Set<string>();
  for (const item of catalog) {
    if (!qualifier) {
      if (!schemas.has(item.schema)) {
        schemas.add(item.schema);
        suggestions.push({
          detail: "schema",
          insertText: insertionIdentifier(item.schema),
          key: `schema:${item.schema}`,
          kind: "schema",
          label: item.schema,
        });
      }
      const tableKey = `${item.schema}\u0000${item.table}`;
      if (!tables.has(tableKey)) {
        tables.add(tableKey);
        suggestions.push({
          detail: item.schema,
          insertText: insertionIdentifier(item.table),
          key: `table:${tableKey}`,
          kind: "table",
          label: item.table,
        });
      }
      const columnKey = `${item.schema}\u0000${item.table}\u0000${item.column}`;
      if (!columns.has(columnKey)) {
        columns.add(columnKey);
        suggestions.push({
          detail: `${item.schema}.${item.table}`,
          insertText: insertionIdentifier(item.column),
          key: `column:${columnKey}`,
          kind: "column",
          label: item.column,
        });
      }
      continue;
    }

    const normalizedSchema = item.schema.toLocaleLowerCase();
    const normalizedTable = item.table.toLocaleLowerCase();
    if (
      normalizedSchema === qualifier &&
      !schemaQualifier &&
      !tables.has(item.table)
    ) {
      tables.add(item.table);
      suggestions.push({
        detail: item.schema,
        insertText: insertionIdentifier(item.table),
        key: `table:${item.schema}\u0000${item.table}`,
        kind: "table",
        label: item.table,
      });
    }
    if (
      normalizedTable === qualifier &&
      (!schemaQualifier || normalizedSchema === schemaQualifier)
    ) {
      const columnKey = `${item.schema}\u0000${item.table}\u0000${item.column}`;
      if (!columns.has(columnKey)) {
        columns.add(columnKey);
        suggestions.push({
          detail: `${item.schema}.${item.table}`,
          insertText: insertionIdentifier(item.column),
          key: `column:${columnKey}`,
          kind: "column",
          label: item.column,
        });
      }
    }
  }
  return suggestions;
};

const kindRank = (kind: PostgresQuerySuggestionKind, tableContext: boolean) => {
  const order: PostgresQuerySuggestionKind[] = tableContext
    ? ["table", "schema", "keyword", "column"]
    : ["keyword", "column", "table", "schema"];
  return order.indexOf(kind);
};

export const postgresQueryCatalogFromResult = (
  result: PostgresQueryResult
): PostgresQueryCatalogColumn[] => {
  const [statement] = result.statements;
  if (!statement) {
    return [];
  }
  const indexes = new Map(
    statement.columns.map((column, index) => [column.name, index])
  );
  const schemaIndex = indexes.get("schema");
  const tableIndex = indexes.get("table");
  const columnIndex = indexes.get("column");
  if (
    schemaIndex === undefined ||
    tableIndex === undefined ||
    columnIndex === undefined
  ) {
    return [];
  }
  return statement.rows.flatMap((row) => {
    const schema = row[schemaIndex]?.text;
    const table = row[tableIndex]?.text;
    const column = row[columnIndex]?.text;
    return schema && table && column ? [{ column, schema, table }] : [];
  });
};

export const postgresQueryCompletion = (
  sql: string,
  cursor: number,
  catalog: PostgresQueryCatalogColumn[],
  explicit = false
): PostgresQueryCompletion => {
  const boundedCursor = Math.max(0, Math.min(sql.length, cursor));
  let tokenStart = boundedCursor;
  while (
    tokenStart > 0 &&
    completionTokenCharacter.test(sql[tokenStart - 1] ?? "")
  ) {
    tokenStart -= 1;
  }
  const token = sql.slice(tokenStart, boundedCursor);
  const parts = token.split(".");
  const finalPart = parts.at(-1) ?? "";
  const prefix = normalizedIdentifier(finalPart);
  const from = boundedCursor - finalPart.length;
  if (!(explicit || prefix || parts.length > 1)) {
    return { from, items: [], to: boundedCursor };
  }

  const qualifier =
    parts.length > 1 ? normalizedIdentifier(parts.at(-2) ?? "") : undefined;
  const schemaQualifier =
    parts.length > 2 ? normalizedIdentifier(parts.at(-3) ?? "") : undefined;
  const context = sql.slice(0, from);
  const tableContext =
    /\b(?:from|join|into|update|table)\s+(?:[\w$".]*\.)?$/iu.test(context);
  const candidates = [
    ...(qualifier ? [] : keywordSuggestions()),
    ...catalogSuggestions(catalog, qualifier, schemaQualifier),
  ];
  const normalizedPrefix = prefix.toLocaleLowerCase();
  const items = candidates
    .filter((candidate) => {
      if (!normalizedPrefix) {
        return true;
      }
      const label = candidate.label.toLocaleLowerCase();
      return (
        label.startsWith(normalizedPrefix) ||
        (normalizedPrefix.length >= 2 && label.includes(normalizedPrefix))
      );
    })
    .toSorted((left, right) => {
      const leftLabel = left.label.toLocaleLowerCase();
      const rightLabel = right.label.toLocaleLowerCase();
      const leftMatch = leftLabel.startsWith(normalizedPrefix) ? 0 : 1;
      const rightMatch = rightLabel.startsWith(normalizedPrefix) ? 0 : 1;
      return (
        leftMatch - rightMatch ||
        kindRank(left.kind, tableContext) -
          kindRank(right.kind, tableContext) ||
        leftLabel.localeCompare(rightLabel) ||
        left.detail.localeCompare(right.detail)
      );
    })
    .slice(0, 8);
  return { from, items, to: boundedCursor };
};
