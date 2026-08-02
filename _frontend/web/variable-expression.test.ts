import { expect, test } from "bun:test";

import { variableReferences } from "@/variable-expression";

const expressionStart = `${String.fromCodePoint(36)}{{`;
const reference = (resource: string, output: string) =>
  `\${{${resource}.${output}}}`;

test("extracts strict variable references from mixed values", () => {
  expect(
    variableReferences(
      `postgres://${expressionStart} database.PGHOST }}:${reference("database", "PGPORT")}/app`
    )
  ).toEqual([
    { output: "PGHOST", resource: "database" },
    { output: "PGPORT", resource: "database" },
  ]);
});

test("rejects malformed and unterminated variable references", () => {
  expect(() => variableReferences(`${expressionStart}database}}`)).toThrow(
    "Invalid variable reference"
  );
  expect(() => variableReferences(`${expressionStart}database.PGHOST`)).toThrow(
    "Unterminated variable reference"
  );
});
