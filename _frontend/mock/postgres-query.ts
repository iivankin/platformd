import type { PostgresQueryResult } from "../web/api";
import { json, readObject, stringField } from "./http";

interface MockPostgresTable {
  columns: {
    hasDefault?: boolean;
    name: string;
    nullable?: boolean;
    typeOid: number;
  }[];
  name: string;
  primaryKeyColumns: string[];
  rows: (null | string)[][];
  schema: string;
}

const mockTables: MockPostgresTable[] = [
  {
    columns: [
      { name: "day", typeOid: 1082 },
      { name: "orders", typeOid: 23 },
      { name: "gross_revenue", typeOid: 1700 },
    ],
    name: "daily_sales",
    primaryKeyColumns: ["day"],
    rows: [
      ["2026-07-29", "184", "12643.20"],
      ["2026-07-30", "221", "15892.40"],
      ["2026-07-31", "207", "14928.10"],
      ["2026-08-01", "249", "18321.90"],
    ],
    schema: "analytics",
  },
  {
    columns: [
      { hasDefault: true, name: "id", nullable: false, typeOid: 20 },
      { name: "name", nullable: false, typeOid: 1043 },
      { name: "email", nullable: false, typeOid: 1043 },
      { hasDefault: true, name: "plan", nullable: false, typeOid: 90_002 },
      { hasDefault: true, name: "active", nullable: false, typeOid: 16 },
      { hasDefault: true, name: "created_at", nullable: false, typeOid: 1184 },
    ],
    name: "customers",
    primaryKeyColumns: ["id"],
    rows: [
      [
        "1001",
        "Ada Lovelace",
        "ada@example.com",
        "scale",
        "true",
        "2026-07-18 09:14:22+00",
      ],
      [
        "1002",
        "Lin Chen",
        "lin@example.com",
        "pro",
        "true",
        "2026-07-19 11:08:45+00",
      ],
      [
        "1003",
        "Marta Silva",
        "marta@example.com",
        "starter",
        "true",
        "2026-07-21 16:32:04+00",
      ],
      [
        "1004",
        "Noah Williams",
        "noah@example.com",
        "pro",
        "false",
        "2026-07-23 08:02:18+00",
      ],
      [
        "1005",
        "Samir Patel",
        "samir@example.com",
        "scale",
        "true",
        "2026-07-24 14:51:57+00",
      ],
      [
        "1006",
        "Elena Petrova",
        "elena@example.com",
        "starter",
        "true",
        "2026-07-27 10:19:31+00",
      ],
      [
        "1007",
        "Jon Bell",
        "jon@example.com",
        "pro",
        "true",
        "2026-07-29 12:44:09+00",
      ],
      [
        "1008",
        "Mae Okafor",
        "mae@example.com",
        "scale",
        "true",
        "2026-08-01 07:25:40+00",
      ],
    ],
    schema: "public",
  },
  {
    columns: [
      { hasDefault: true, name: "id", nullable: false, typeOid: 2950 },
      { name: "customer_id", nullable: false, typeOid: 20 },
      { name: "status", nullable: false, typeOid: 90_001 },
      { name: "total", nullable: false, typeOid: 1700 },
      { hasDefault: true, name: "currency", nullable: false, typeOid: 1042 },
      { hasDefault: true, name: "placed_at", nullable: false, typeOid: 1184 },
    ],
    name: "orders",
    primaryKeyColumns: ["id"],
    rows: [
      [
        "f9a1b942-7b35-4ac5-8168-b673bf627610",
        "1005",
        "paid",
        "249.00",
        "USD",
        "2026-08-01 08:14:12+00",
      ],
      [
        "2dcb02a6-26ca-4bd7-93e8-36957d0dc28f",
        "1001",
        "fulfilled",
        "84.50",
        "USD",
        "2026-08-01 09:02:44+00",
      ],
      [
        "57399a31-fbdb-42fa-a35f-e1f6c19d1f60",
        "1008",
        "paid",
        "129.90",
        "EUR",
        "2026-08-01 10:37:03+00",
      ],
      [
        "a46ca6d8-5a91-45f0-8ae6-b6788e139f61",
        "1003",
        "refunded",
        "42.00",
        "USD",
        "2026-08-01 11:21:36+00",
      ],
      [
        "d85fbff1-8ac9-4ec0-9c11-0cd2673a621c",
        "1002",
        "fulfilled",
        "318.40",
        "USD",
        "2026-08-01 12:46:50+00",
      ],
    ],
    schema: "public",
  },
  {
    columns: [
      { hasDefault: true, name: "id", nullable: false, typeOid: 23 },
      { name: "sku", nullable: false, typeOid: 1043 },
      { name: "name", nullable: false, typeOid: 1043 },
      { name: "price", nullable: false, typeOid: 1700 },
      { hasDefault: true, name: "inventory", nullable: false, typeOid: 23 },
      { name: "metadata", nullable: true, typeOid: 3802 },
    ],
    name: "products",
    primaryKeyColumns: ["id"],
    rows: [
      ["1", "RD-100", "Signal Mug", "18.00", "148", '{"color":"stone"}'],
      [
        "2",
        "RD-220",
        "Operator Hoodie",
        "72.00",
        "36",
        '{"sizeRange":"XS-XL"}',
      ],
      ["3", "RD-305", "Deploy Notebook", "14.50", "213", '{"pages":192}'],
      ["4", "RD-410", "Rack Keycap", "9.00", "504", null],
      ["5", "RD-520", "Incident Timer", "39.00", "81", '{"battery":"AAA"}'],
      ["6", "RD-610", "Uptime Pin", "6.00", "925", null],
    ],
    schema: "public",
  },
  {
    columns: [
      { name: "id", typeOid: 23 },
      { name: "order_id", typeOid: 2950 },
      { name: "product_id", typeOid: 23 },
      { name: "quantity", typeOid: 23 },
      { name: "unit_price", typeOid: 1700 },
    ],
    name: "order_items",
    primaryKeyColumns: ["id"],
    rows: [
      ["1", "f9a1b942-7b35-4ac5-8168-b673bf627610", "2", "1", "72.00"],
      ["2", "f9a1b942-7b35-4ac5-8168-b673bf627610", "1", "2", "18.00"],
      ["3", "2dcb02a6-26ca-4bd7-93e8-36957d0dc28f", "3", "1", "14.50"],
      ["4", "57399a31-fbdb-42fa-a35f-e1f6c19d1f60", "5", "1", "39.00"],
      ["5", "d85fbff1-8ac9-4ec0-9c11-0cd2673a621c", "2", "2", "72.00"],
    ],
    schema: "public",
  },
];

const mockTableSeed = mockTables.map((table) => ({
  ...table,
  rows: table.rows.map((row) => [...row]),
}));

export const resetMockPostgresData = () => {
  for (const [index, table] of mockTables.entries()) {
    const seed = mockTableSeed[index];
    if (!seed) {
      continue;
    }
    table.rows.length = 0;
    table.rows.push(...seed.rows.map((row) => [...row]));
  }
};

interface MockPostgresForeignKey {
  columns: string[];
  foreignColumns: string[];
  foreignSchema: string;
  foreignTable: string;
  name: string;
  schema: string;
  table: string;
}

const mockForeignKeys: MockPostgresForeignKey[] = [
  {
    columns: ["customer_id"],
    foreignColumns: ["id"],
    foreignSchema: "public",
    foreignTable: "customers",
    name: "orders_customer_id_fkey",
    schema: "public",
    table: "orders",
  },
  {
    columns: ["order_id"],
    foreignColumns: ["id"],
    foreignSchema: "public",
    foreignTable: "orders",
    name: "order_items_order_id_fkey",
    schema: "public",
    table: "order_items",
  },
  {
    columns: ["product_id"],
    foreignColumns: ["id"],
    foreignSchema: "public",
    foreignTable: "products",
    name: "order_items_product_id_fkey",
    schema: "public",
    table: "order_items",
  },
];

const mockEnums = [
  {
    labels: ["paid", "fulfilled", "refunded", "pending"],
    name: "order_status",
    typeOID: 90_001,
  },
  {
    labels: ["starter", "pro", "scale"],
    name: "customer_plan",
    typeOID: 90_002,
  },
];

const result = (
  columns: MockPostgresTable["columns"],
  rows: MockPostgresTable["rows"]
): PostgresQueryResult => ({
  auditRecorded: true,
  statements: [
    {
      columns,
      commandTag: `SELECT ${rows.length.toString()}`,
      rows: rows.map((row) =>
        row.map((value) => (value === null ? { null: true } : { text: value }))
      ),
      truncated: false,
    },
  ],
  truncated: false,
});

const catalogResult = () =>
  result(
    [
      { name: "schema", typeOid: 25 },
      { name: "table", typeOid: 25 },
      { name: "approximate_rows", typeOid: 25 },
      { name: "primary_key_columns", typeOid: 25 },
    ],
    mockTables.map((table) => [
      table.schema,
      table.name,
      table.rows.length.toString(),
      JSON.stringify(table.primaryKeyColumns),
    ])
  );

const foreignKeyCatalogResult = () =>
  result(
    [
      { name: "schema", typeOid: 25 },
      { name: "table", typeOid: 25 },
      { name: "foreign_schema", typeOid: 25 },
      { name: "foreign_table", typeOid: 25 },
      { name: "name", typeOid: 25 },
      { name: "columns", typeOid: 25 },
      { name: "foreign_columns", typeOid: 25 },
    ],
    mockForeignKeys.map((foreignKey) => [
      foreignKey.schema,
      foreignKey.table,
      foreignKey.foreignSchema,
      foreignKey.foreignTable,
      foreignKey.name,
      JSON.stringify(foreignKey.columns),
      JSON.stringify(foreignKey.foreignColumns),
    ])
  );

const enumCatalogResult = () =>
  result(
    [
      { name: "type_oid", typeOid: 25 },
      { name: "type_name", typeOid: 25 },
      { name: "labels", typeOid: 25 },
    ],
    mockEnums.map((enumType) => [
      enumType.typeOID.toString(),
      enumType.name,
      JSON.stringify(enumType.labels),
    ])
  );

const columnMetaResult = (sql: string) => {
  const source =
    /nspname = '(?<schema>(?:[^']|'')*)'[\s\S]*?relname = '(?<table>(?:[^']|'')*)'/iu.exec(
      sql
    );
  const schema = (source?.groups?.schema ?? "").replaceAll("''", "'");
  const name = (source?.groups?.table ?? "").replaceAll("''", "'");
  const table = mockTables.find(
    (candidate) => candidate.schema === schema && candidate.name === name
  );
  return result(
    [
      { name: "name", typeOid: 25 },
      { name: "type_oid", typeOid: 25 },
      { name: "nullable", typeOid: 25 },
      { name: "has_default", typeOid: 25 },
    ],
    (table?.columns ?? []).map((column) => [
      column.name,
      column.typeOid.toString(),
      column.nullable === false ? "false" : "true",
      column.hasDefault ? "true" : "false",
    ])
  );
};

const unquoteIdentifier = (value: string) => value.replaceAll('""', '"');

const tableFromSQL = (sql: string) => {
  const source =
    /FROM\s+"(?<schema>(?:[^"]|"")+)"\."(?<table>(?:[^"]|"")+)"/iu.exec(sql);
  if (!source) {
    return;
  }
  const schema = unquoteIdentifier(source.groups?.schema ?? "");
  const name = unquoteIdentifier(source.groups?.table ?? "");
  return mockTables.find(
    (candidate) => candidate.schema === schema && candidate.name === name
  );
};

const unquoteLiteral = (value: string) =>
  value.startsWith("'") && value.endsWith("'")
    ? value.slice(1, -1).replaceAll("''", "'")
    : value;

const likePattern = (value: string) =>
  new RegExp(
    `^${value
      .replaceAll(/[.*+?^${}()|[\]\\]/gu, "\\$&")
      .replaceAll("%", ".*")
      .replaceAll("_", ".")}$`,
    "u"
  );

const comparisonMatches = (
  operator: string | undefined,
  comparison: number
) => {
  switch (operator) {
    case "=": {
      return comparison === 0;
    }
    case "<>": {
      return comparison !== 0;
    }
    case ">": {
      return comparison > 0;
    }
    case ">=": {
      return comparison >= 0;
    }
    case "<": {
      return comparison < 0;
    }
    default: {
      return comparison <= 0;
    }
  }
};

const likeMatches = (
  operator: string | undefined,
  cell: string,
  value: string
) => {
  if (operator === "ILIKE") {
    return likePattern(value.toLowerCase()).test(cell.toLowerCase());
  }
  const matches = likePattern(value).test(cell);
  return operator === "LIKE" ? matches : !matches;
};

const conditionMatches = (
  operator: string | undefined,
  cell: null | string | undefined,
  rawValue: string
) => {
  if (operator === "IS NULL") {
    return cell === null;
  }
  if (operator === "IS NOT NULL") {
    return cell !== null && cell !== undefined;
  }
  if (cell === null || cell === undefined) {
    return false;
  }
  if (operator === "IN") {
    const values = rawValue
      .replaceAll(/^\(|\)$/gu, "")
      .split(",")
      .map((value) => unquoteLiteral(value.trim()));
    return values.includes(cell);
  }
  const value = unquoteLiteral(rawValue);
  if (operator === "LIKE" || operator === "NOT LIKE" || operator === "ILIKE") {
    return likeMatches(operator, cell, value);
  }
  const leftNumber = Number(cell);
  const rightNumber = Number(value);
  const comparison =
    Number.isFinite(leftNumber) && Number.isFinite(rightNumber)
      ? leftNumber - rightNumber
      : cell.localeCompare(value);
  return comparisonMatches(operator, comparison);
};

const rowMatchesCondition = (
  row: (null | string)[],
  table: MockPostgresTable,
  condition: string
) => {
  const match =
    /^(?:(?<connector>AND|OR)\s+)?"(?<column>(?:[^"]|"")+)"\s+(?<operator>IS NOT NULL|IS NULL|NOT LIKE|ILIKE|LIKE|IN|<>|>=|<=|=|>|<)(?:\s+(?<value>.+))?$/iu.exec(
      condition.trim()
    );
  if (!match) {
    return { connector: "and", matches: true };
  }
  const connector =
    match.groups?.connector?.toLowerCase() === "or" ? "or" : "and";
  const column = unquoteIdentifier(match.groups?.column ?? "");
  const columnIndex = table.columns.findIndex(
    (candidate) => candidate.name === column
  );
  const cell = row[columnIndex];
  const operator = match.groups?.operator?.toUpperCase();
  const rawValue = match.groups?.value?.trim() ?? "";
  return {
    connector,
    matches: columnIndex !== -1 && conditionMatches(operator, cell, rawValue),
  };
};

const filteredRows = (sql: string, table: MockPostgresTable) => {
  const where = /WHERE\s+(?<where>[\s\S]*?)(?:\nORDER BY|\nLIMIT|;)/iu.exec(sql)
    ?.groups?.where;
  if (!where) {
    return [...table.rows];
  }
  const conditions = where.split(/\n\s+(?=AND|OR)/iu);
  return table.rows.filter((row) => {
    let included = false;
    for (const [index, condition] of conditions.entries()) {
      const matchResult = rowMatchesCondition(row, table, condition);
      if (index === 0) {
        included = matchResult.matches;
      } else if (matchResult.connector === "or") {
        included ||= matchResult.matches;
      } else {
        included &&= matchResult.matches;
      }
    }
    return included;
  });
};

const tableResult = (sql: string) => {
  const table = tableFromSQL(sql);
  if (!table) {
    return result([], []);
  }

  const orderedRows = filteredRows(sql, table);
  const order =
    /ORDER BY\s+"(?<column>(?:[^"]|"")+)"\s+(?<direction>ASC|DESC)/iu.exec(sql);
  if (order) {
    const column = unquoteIdentifier(order.groups?.column ?? "");
    const columnIndex = table.columns.findIndex(
      (candidate) => candidate.name === column
    );
    if (columnIndex !== -1) {
      const direction = order.groups?.direction === "DESC" ? -1 : 1;
      orderedRows.sort(
        (left, right) =>
          (left[columnIndex] ?? "").localeCompare(
            right[columnIndex] ?? "",
            undefined,
            {
              numeric: true,
            }
          ) * direction
      );
    }
  }
  const offset = Math.trunc(
    Number(/OFFSET\s+(?<offset>\d+)/iu.exec(sql)?.groups?.offset ?? "0")
  );
  const limit = Math.trunc(
    Number(/LIMIT\s+(?<limit>\d+)/iu.exec(sql)?.groups?.limit ?? "51")
  );
  return result(table.columns, orderedRows.slice(offset, offset + limit));
};

const countResult = (sql: string) => {
  const table = tableFromSQL(sql);
  return result(
    [{ name: "count", typeOid: 25 }],
    [[table ? filteredRows(sql, table).length.toString() : "0"]]
  );
};

const mutationResult = (commandTag: string): PostgresQueryResult => ({
  auditRecorded: true,
  statements: [
    {
      columns: [],
      commandTag,
      rows: [],
      truncated: false,
    },
  ],
  truncated: false,
});

const parseAssignmentValue = (raw: string): null | string => {
  const trimmed = raw.trim();
  if (trimmed.toUpperCase() === "NULL") {
    return null;
  }
  if (trimmed.toUpperCase() === "TRUE") {
    return "true";
  }
  if (trimmed.toUpperCase() === "FALSE") {
    return "false";
  }
  if (
    (trimmed.startsWith("'") && trimmed.endsWith("'")) ||
    /^-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?$/u.test(trimmed)
  ) {
    return unquoteLiteral(trimmed);
  }
  return trimmed;
};

const rowMatchesMutationWhere = (
  row: (null | string)[],
  table: MockPostgresTable,
  where: string
) => {
  const clauses = where
    .replaceAll(/;?\s*$/gu, "")
    .split(/\n\s*OR\s+/iu)
    .map((clause) =>
      clause
        .trim()
        .replaceAll(/^\(|\)$/gu, "")
        .trim()
    );
  return clauses.some((clause) => {
    const parts = clause.split(/\s+AND\s+/iu);
    return parts.every(
      (part) => rowMatchesCondition(row, table, part.trim()).matches
    );
  });
};

const updateResult = (sql: string) => {
  const match =
    /UPDATE\s+"(?<schema>(?:[^"]|"")+)"\."(?<table>(?:[^"]|"")+)"\s+SET\s+"(?<column>(?:[^"]|"")+)"\s*=\s*(?<value>NULL|TRUE|FALSE|'(?:[^']|'')*'|-?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?)\s+WHERE\s+(?<where>[\s\S]+)$/iu.exec(
      sql
    );
  const groups = match?.groups;
  if (!groups) {
    return mutationResult("UPDATE 0");
  }
  const schema = unquoteIdentifier(groups.schema ?? "");
  const name = unquoteIdentifier(groups.table ?? "");
  const column = unquoteIdentifier(groups.column ?? "");
  const table = mockTables.find(
    (candidate) => candidate.schema === schema && candidate.name === name
  );
  if (!table) {
    return mutationResult("UPDATE 0");
  }
  const columnIndex = table.columns.findIndex(
    (candidate) => candidate.name === column
  );
  if (columnIndex === -1) {
    return mutationResult("UPDATE 0");
  }
  const nextValue = parseAssignmentValue(groups.value ?? "");
  let updated = 0;
  for (const row of table.rows) {
    if (!rowMatchesMutationWhere(row, table, groups.where ?? "")) {
      continue;
    }
    row[columnIndex] = nextValue;
    updated += 1;
  }
  return mutationResult(`UPDATE ${updated.toString()}`);
};

const deleteResult = (sql: string) => {
  const match =
    /DELETE FROM\s+"(?<schema>(?:[^"]|"")+)"\."(?<table>(?:[^"]|"")+)"\s+WHERE\s+(?<where>[\s\S]+)$/iu.exec(
      sql
    );
  const groups = match?.groups;
  if (!groups) {
    return mutationResult("DELETE 0");
  }
  const schema = unquoteIdentifier(groups.schema ?? "");
  const name = unquoteIdentifier(groups.table ?? "");
  const table = mockTables.find(
    (candidate) => candidate.schema === schema && candidate.name === name
  );
  if (!table) {
    return mutationResult("DELETE 0");
  }
  const remaining = table.rows.filter(
    (row) => !rowMatchesMutationWhere(row, table, groups.where ?? "")
  );
  const deleted = table.rows.length - remaining.length;
  table.rows.length = 0;
  table.rows.push(...remaining);
  return mutationResult(`DELETE ${deleted.toString()}`);
};

const parseInsertValueList = (raw: string) =>
  raw
    .split(",")
    .map((part) => part.trim())
    .map((part) => parseAssignmentValue(part));

const insertResult = (sql: string) => {
  const defaultMatch =
    /INSERT INTO\s+"(?<schema>(?:[^"]|"")+)"\."(?<table>(?:[^"]|"")+)"\s+DEFAULT VALUES/iu.exec(
      sql
    );
  if (defaultMatch?.groups) {
    const schema = unquoteIdentifier(defaultMatch.groups.schema ?? "");
    const name = unquoteIdentifier(defaultMatch.groups.table ?? "");
    const table = mockTables.find(
      (candidate) => candidate.schema === schema && candidate.name === name
    );
    if (!table) {
      return mutationResult("INSERT 0 0");
    }
    table.rows.push(table.columns.map(() => null));
    return mutationResult("INSERT 0 1");
  }

  const match =
    /INSERT INTO\s+"(?<schema>(?:[^"]|"")+)"\."(?<table>(?:[^"]|"")+)"\s*\(\s*(?<columns>[\s\S]*?)\s*\)\s*VALUES\s*\(\s*(?<values>[\s\S]*?)\s*\)/iu.exec(
      sql
    );
  const groups = match?.groups;
  if (!groups) {
    return mutationResult("INSERT 0 0");
  }
  const schema = unquoteIdentifier(groups.schema ?? "");
  const name = unquoteIdentifier(groups.table ?? "");
  const table = mockTables.find(
    (candidate) => candidate.schema === schema && candidate.name === name
  );
  if (!table) {
    return mutationResult("INSERT 0 0");
  }
  const columnNames = (groups.columns ?? "")
    .split(",")
    .map((part) => unquoteIdentifier(part.trim().replaceAll(/^"|"$/gu, "")));
  const values = parseInsertValueList(groups.values ?? "");
  const row = table.columns.map((column) => {
    const index = columnNames.indexOf(column.name);
    if (index === -1) {
      return null;
    }
    return values[index] ?? null;
  });
  table.rows.push(row);
  return mutationResult("INSERT 0 1");
};

export const handlePostgresQuery = async (
  request: Request,
  collection: string,
  rest: string[]
): Promise<Response | undefined> => {
  const [resource, ...tail] = rest;
  if (
    request.method !== "POST" ||
    collection !== "postgres" ||
    resource !== "query" ||
    tail.length > 0
  ) {
    return undefined;
  }
  const input = await readObject(request);
  const sql = stringField(input, "sql");

  // Tagged comments keep the demo handler deterministic without pretending to
  // parse arbitrary SQL. Production still executes the generated statement.
  if (sql.includes("platformd:data-browser:catalog")) {
    return json(catalogResult());
  }
  if (sql.includes("platformd:data-browser:foreign-keys")) {
    return json(foreignKeyCatalogResult());
  }
  if (sql.includes("platformd:data-browser:enums")) {
    return json(enumCatalogResult());
  }
  if (sql.includes("platformd:data-browser:columns")) {
    return json(columnMetaResult(sql));
  }
  if (sql.includes("platformd:data-browser:table")) {
    return json(tableResult(sql));
  }
  if (sql.includes("platformd:data-browser:count")) {
    return json(countResult(sql));
  }
  if (sql.includes("platformd:data-browser:update")) {
    return json(updateResult(sql));
  }
  if (sql.includes("platformd:data-browser:delete")) {
    return json(deleteResult(sql));
  }
  if (sql.includes("platformd:data-browser:insert")) {
    return json(insertResult(sql));
  }
  return json(
    result([{ name: "status", typeOid: 25 }], [["mock backend ready"]])
  );
};
