import type { PostgresQueryResult } from "../web/api";
import { json, readObject, stringField } from "./http";

interface MockPostgresTable {
  columns: { name: string; typeOid: number }[];
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
      { name: "id", typeOid: 20 },
      { name: "name", typeOid: 1043 },
      { name: "email", typeOid: 1043 },
      { name: "plan", typeOid: 25 },
      { name: "active", typeOid: 16 },
      { name: "created_at", typeOid: 1184 },
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
      { name: "id", typeOid: 2950 },
      { name: "customer_id", typeOid: 20 },
      { name: "status", typeOid: 1043 },
      { name: "total", typeOid: 1700 },
      { name: "currency", typeOid: 1042 },
      { name: "placed_at", typeOid: 1184 },
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
      { name: "id", typeOid: 23 },
      { name: "sku", typeOid: 1043 },
      { name: "name", typeOid: 1043 },
      { name: "price", typeOid: 1700 },
      { name: "inventory", typeOid: 23 },
      { name: "metadata", typeOid: 3802 },
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
  // parse arbitrary SQL. Production still executes the generated SELECT query.
  if (sql.includes("platformd:data-browser:catalog")) {
    return json(catalogResult());
  }
  if (sql.includes("platformd:data-browser:table")) {
    return json(tableResult(sql));
  }
  if (sql.includes("platformd:data-browser:count")) {
    return json(countResult(sql));
  }
  return json(
    result([{ name: "status", typeOid: 25 }], [["mock backend ready"]])
  );
};
