import { describe, expect, test } from "bun:test";

import {
  activePostgresFilters,
  postgresCountFromResult,
  postgresTableCountSQL,
  postgresTableDataSQL,
  postgresTablesFromResult,
  quotePostgresIdentifier,
  quotePostgresLiteral,
} from "@/postgres-data-browser-model";

const table = {
  approximateRows: 830,
  name: 'events"archive',
  primaryKeyColumns: ["tenant_id", "event_id"],
  schema: "audit",
};

describe("PostgreSQL data browser queries", () => {
  test("quotes identifiers and literals", () => {
    expect(quotePostgresIdentifier('odd"name')).toBe('"odd""name"');
    expect(quotePostgresLiteral("O'Reilly")).toBe("'O''Reilly'");
  });

  test("uses stable primary-key order and bounds the page", () => {
    expect(postgresTableDataSQL({ page: 2, table })).toContain(
      'FROM "audit"."events""archive"\nORDER BY "tenant_id" ASC, "event_id" ASC\nLIMIT 51\nOFFSET 100'
    );
  });

  test("applies explicit sorting and escaped filters", () => {
    const filters = [
      {
        column: "actor",
        connector: "and" as const,
        id: "filter-1",
        operator: "=" as const,
        value: "O'Reilly",
      },
      {
        column: "status",
        connector: "or" as const,
        id: "filter-2",
        operator: "in" as const,
        value: "open, closed",
      },
    ];
    const sql = postgresTableDataSQL({
      filters,
      page: 0,
      sort: { column: 'created"at', direction: "desc" },
      table,
    });
    expect(sql).toContain(
      "WHERE \"actor\" = 'O''Reilly'\n  OR \"status\" IN ('open', 'closed')"
    );
    expect(sql).toContain('ORDER BY "created""at" DESC');
    expect(postgresTableCountSQL({ filters, table })).toContain(
      "WHERE \"actor\" = 'O''Reilly'\n  OR \"status\" IN ('open', 'closed')"
    );
  });

  test("does not execute incomplete filters", () => {
    expect(
      activePostgresFilters([
        {
          column: "status",
          connector: "and",
          id: "filter-1",
          operator: "in",
          value: ", ,",
        },
      ])
    ).toEqual([]);
  });

  test("reads table metadata and primary-key order by column name", () => {
    expect(
      postgresTablesFromResult({
        auditRecorded: true,
        statements: [
          {
            columns: [
              { name: "table", typeOid: 25 },
              { name: "primary_key_columns", typeOid: 25 },
              { name: "approximate_rows", typeOid: 25 },
              { name: "schema", typeOid: 25 },
            ],
            commandTag: "SELECT 1",
            rows: [
              [
                { text: "orders" },
                { text: '["tenant_id","id"]' },
                { text: "830" },
                { text: "public" },
              ],
            ],
            truncated: false,
          },
        ],
        truncated: false,
      })
    ).toEqual([
      {
        approximateRows: 830,
        name: "orders",
        primaryKeyColumns: ["tenant_id", "id"],
        schema: "public",
      },
    ]);
  });

  test("reads a safe exact count", () => {
    expect(
      postgresCountFromResult({
        auditRecorded: true,
        statements: [
          {
            columns: [{ name: "count", typeOid: 25 }],
            commandTag: "SELECT 1",
            rows: [[{ text: "2155" }]],
            truncated: false,
          },
        ],
        truncated: false,
      })
    ).toBe(2155);
  });
});
