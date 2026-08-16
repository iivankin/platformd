import { describe, expect, test } from "bun:test";

import type { LogRecord } from "@/api";
import { formatLogExport } from "@/log-export";

const record: LogRecord = {
  attemptId: "attempt-1",
  deploymentId: "deployment-1",
  fields: { caller: "server/http.go:84", request: { method: "GET" } },
  severityText: "INFO",
  stream: "stdout",
  text: '\u001B[32mready, "now"\u001B[0m',
  timestamp: "2026-08-16T03:00:00.000Z",
  traceId: "0123456789abcdef0123456789abcdef",
};

describe("log export", () => {
  test("plain text removes terminal control sequences but keeps context", () => {
    const output = formatLogExport([record], "text");

    expect(output).not.toContain("\u001B");
    expect(output).toContain("INFO");
    expect(output).toContain("deployment=deployment-1");
    expect(output).toContain('ready, "now"');
    expect(output).toContain('"caller":"server/http.go:84"');
  });

  test("CSV escapes messages and preserves structured fields", () => {
    const output = formatLogExport([record], "csv");

    expect(output.split("\n")).toHaveLength(2);
    expect(output).toContain('"ready, ""now"""');
    expect(output).toContain('"{""caller"":""server/http.go:84""');
  });

  test("JSON preserves the original structured record", () => {
    const output = JSON.parse(formatLogExport([record], "json"));

    expect(output).toEqual([record]);
  });
});
