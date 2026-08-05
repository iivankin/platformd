import { expect, test } from "bun:test";

import {
  applyParsedEnvironment,
  looksLikeServiceEnvironment,
  parseServiceEnvironment,
} from "@/service-environment";

test("parseServiceEnvironment accepts blank lines and comments", () => {
  expect(
    parseServiceEnvironment(`
# database
DATABASE_URL=postgres://localhost/db

# token with equals in the value
HF_TOKEN=abc=def
`)
  ).toEqual({
    DATABASE_URL: "postgres://localhost/db",
    HF_TOKEN: "abc=def",
  });
});

test("parseServiceEnvironment accepts export prefixes and CRLF", () => {
  expect(parseServiceEnvironment("export FOO=1\r\nBAR=2\r\n")).toEqual({
    BAR: "2",
    FOO: "1",
  });
});

test("parseServiceEnvironment rejects invalid lines", () => {
  expect(() => parseServiceEnvironment("not a variable")).toThrow(
    "Invalid environment line: not a variable"
  );
});

test("looksLikeServiceEnvironment detects KEY=VALUE paste", () => {
  expect(looksLikeServiceEnvironment("HF_TOKEN=secret")).toBe(true);
  expect(looksLikeServiceEnvironment("HF_TOKEN")).toBe(false);
  expect(looksLikeServiceEnvironment("1BAD=value")).toBe(false);
});

test("applyParsedEnvironment fills the preferred empty row then upserts", () => {
  const rows = [
    { id: "empty", name: "", value: "" },
    { id: "existing", name: "HF_TOKEN", value: "old" },
  ];

  expect(
    applyParsedEnvironment(
      rows,
      {
        DATABASE_URL: "postgres://db",
        HF_TOKEN: "new",
        NEW_VAR: "1",
      },
      "empty"
    )
  ).toEqual([
    { id: expect.any(String), name: "NEW_VAR", value: "1" },
    { id: "empty", name: "DATABASE_URL", value: "postgres://db" },
    { id: "existing", name: "HF_TOKEN", value: "new" },
  ]);
});
