import { describe, expect, test } from "bun:test";

import {
  activePostgresFilters,
  postgresCountFromResult,
  postgresForeignKeysFromResult,
  postgresOutgoingRelationForColumn,
  postgresRelationNavigation,
  postgresRelationsForTable,
  postgresTableCountSQL,
  postgresTableDataSQL,
  postgresTablesFromResult,
  quotePostgresIdentifier,
  quotePostgresLiteral,
} from "@/postgres-data-browser-model";
import { postgresCellDisplay } from "@/postgres-data-cell";

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

  test("reads foreign keys and builds relation navigation", () => {
    const foreignKeys = postgresForeignKeysFromResult({
      auditRecorded: true,
      statements: [
        {
          columns: [
            { name: "schema", typeOid: 25 },
            { name: "table", typeOid: 25 },
            { name: "foreign_schema", typeOid: 25 },
            { name: "foreign_table", typeOid: 25 },
            { name: "name", typeOid: 25 },
            { name: "columns", typeOid: 25 },
            { name: "foreign_columns", typeOid: 25 },
          ],
          commandTag: "SELECT 2",
          rows: [
            [
              { text: "public" },
              { text: "orders" },
              { text: "public" },
              { text: "customers" },
              { text: "orders_customer_id_fkey" },
              { text: '["customer_id"]' },
              { text: '["id"]' },
            ],
            [
              { text: "public" },
              { text: "products" },
              { text: "public" },
              { text: "categories" },
              { text: "products_category_id_fkey" },
              { text: '["category_id"]' },
              { text: '["id"]' },
            ],
          ],
          truncated: false,
        },
      ],
      truncated: false,
    });
    expect(foreignKeys).toEqual([
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
        columns: ["category_id"],
        foreignColumns: ["id"],
        foreignSchema: "public",
        foreignTable: "categories",
        name: "products_category_id_fkey",
        schema: "public",
        table: "products",
      },
    ]);

    const categoryRelations = postgresRelationsForTable(foreignKeys, {
      name: "categories",
      primaryKeyColumns: ["id"],
      schema: "public",
    });
    const [, productsForeignKey] = foreignKeys;
    expect(productsForeignKey).toBeDefined();
    if (!productsForeignKey) {
      throw new Error("expected products foreign key");
    }
    expect(categoryRelations).toEqual([
      {
        direction: "incoming",
        foreignKey: productsForeignKey,
        key: "in:products_category_id_fkey",
        label: "products",
      },
    ]);

    const orderRelations = postgresRelationsForTable(foreignKeys, {
      name: "orders",
      primaryKeyColumns: ["id"],
      schema: "public",
    });
    const customerRelation = postgresOutgoingRelationForColumn(
      orderRelations,
      "customer_id"
    );
    expect(customerRelation).toBeDefined();
    if (!customerRelation) {
      throw new Error("expected customer relation");
    }
    expect(customerRelation.label).toBe("customers");
    const navigation = postgresRelationNavigation({
      columnValues: { customer_id: "42" },
      relation: customerRelation,
    });
    expect(navigation).toMatchObject({
      filters: [{ column: "id", operator: "=", value: "42" }],
      schema: "public",
      table: "customers",
    });
  });
});

describe("PostgreSQL cell display", () => {
  test("formats null, empty, json, and binary values", () => {
    expect(postgresCellDisplay({ null: true })).toEqual({
      kind: "null",
      preview: "NULL",
    });
    expect(postgresCellDisplay({ text: "" })).toEqual({
      kind: "empty",
      preview: "EMPTY",
    });
    expect(postgresCellDisplay({ text: '{"a":1}' }, 3802)).toMatchObject({
      formatted: '{\n  "a": 1\n}',
      kind: "json",
    });
    expect(postgresCellDisplay({ base64: "aGVsbG8=" })).toMatchObject({
      kind: "binary",
      sizeLabel: "5 B",
    });
  });
});
