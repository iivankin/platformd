import { parseAnsi } from "@/ansi-text";
import type { LogRecord } from "@/api";
import { logSeverity } from "@/log-severity";

export type LogExportFormat = "csv" | "json" | "text";

const plainMessage = (record: LogRecord) =>
  parseAnsi(record.text)
    .map((segment) => segment.text)
    .join("");

const csvCell = (value: unknown) =>
  `"${String(value ?? "").replaceAll('"', '""')}"`;

const csvRecord = (record: LogRecord) =>
  [
    record.timestamp,
    record.severityText || logSeverity(record),
    record.deploymentId,
    record.attemptId,
    record.stream,
    record.phase ?? "",
    record.traceId ?? "",
    record.spanId ?? "",
    plainMessage(record),
    record.fields ? JSON.stringify(record.fields) : "",
  ]
    .map(csvCell)
    .join(",");

export const formatLogExport = (
  records: LogRecord[],
  format: LogExportFormat
) => {
  if (format === "json") {
    return `${JSON.stringify(records, null, 2)}\n`;
  }
  if (format === "csv") {
    return [
      "timestamp,severity,deployment_id,attempt_id,stream,phase,trace_id,span_id,message,fields",
      ...records.map(csvRecord),
    ].join("\n");
  }
  return records
    .map((record) => {
      const context = [
        record.deploymentId ? `deployment=${record.deploymentId}` : "",
        record.traceId ? `trace=${record.traceId}` : "",
        record.spanId ? `span=${record.spanId}` : "",
      ].filter(Boolean);
      const fields = record.fields ? ` ${JSON.stringify(record.fields)}` : "";
      return `${record.timestamp} ${(
        record.severityText || logSeverity(record)
      ).toUpperCase()}${context.length ? ` [${context.join(" ")}]` : ""} ${plainMessage(record)}${fields}`;
    })
    .join("\n");
};

export const logExportExtension = (format: LogExportFormat) =>
  format === "text" ? "log" : format;

export const logExportMime = (format: LogExportFormat) => {
  if (format === "json") {
    return "application/json;charset=utf-8";
  }
  if (format === "csv") {
    return "text/csv;charset=utf-8";
  }
  return "text/plain;charset=utf-8";
};
