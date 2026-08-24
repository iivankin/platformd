import { describe, expect, test } from "bun:test";

import type { PostgresQueryResult } from "@/api";
import {
  postgresQueryCatalogFromResult,
  postgresQueryCompletion,
} from "@/postgres-query-suggestions";

const catalogResult: PostgresQueryResult = {
  auditRecorded: true,
  statements: [
    {
      columns: [
        { name: "schema", typeOid: 25 },
        { name: "table", typeOid: 25 },
        { name: "column", typeOid: 25 },
      ],
      commandTag: "SELECT 3",
      rows: [
        [{ text: "public" }, { text: "users" }, { text: "id" }],
        [{ text: "public" }, { text: "users" }, { text: "email" }],
        [{ text: "billing" }, { text: "Order Items" }, { text: "total" }],
      ],
      truncated: false,
    },
  ],
  truncated: false,
};

describe("PostgreSQL query suggestions", () => {
  const catalog = postgresQueryCatalogFromResult(catalogResult);

  test("parses the live schema catalog", () => {
    expect(catalog).toEqual([
      { column: "id", schema: "public", table: "users" },
      { column: "email", schema: "public", table: "users" },
      { column: "total", schema: "billing", table: "Order Items" },
    ]);
  });

  test("suggests SQL keywords and prioritizes tables after FROM", () => {
    expect(postgresQueryCompletion("SEL", 3, catalog).items[0]?.label).toBe(
      "SELECT"
    );
    const sql = "SELECT * FROM us";
    expect(
      postgresQueryCompletion(sql, sql.length, catalog).items[0]
    ).toMatchObject({ kind: "table", label: "users" });
  });

  test("completes schema-qualified tables and table-qualified columns", () => {
    const tableSQL = "SELECT * FROM public.us";
    const tableCompletion = postgresQueryCompletion(
      tableSQL,
      tableSQL.length,
      catalog
    );
    expect(tableCompletion.items[0]).toMatchObject({
      insertText: "users",
      kind: "table",
    });
    expect(tableSQL.slice(tableCompletion.from)).toBe("us");

    const columnSQL = "SELECT users.em";
    const columnCompletion = postgresQueryCompletion(
      columnSQL,
      columnSQL.length,
      catalog
    );
    expect(columnCompletion.items[0]).toMatchObject({
      insertText: "email",
      kind: "column",
    });
    expect(columnSQL.slice(columnCompletion.from)).toBe("em");

    const trailingDotSQL = "SELECT users.";
    expect(
      postgresQueryCompletion(trailingDotSQL, trailingDotSQL.length, catalog)
        .items[0]
    ).toMatchObject({ kind: "column", label: "email" });
  });

  test("quotes identifiers that PostgreSQL cannot safely fold", () => {
    const sql = "SELECT * FROM ord";
    expect(
      postgresQueryCompletion(sql, sql.length, catalog).items[0]
    ).toMatchObject({
      insertText: '"Order Items"',
      kind: "table",
    });
  });
});
