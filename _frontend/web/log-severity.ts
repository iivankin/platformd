import type { LogRecord } from "@/api";

export const logSeverityValues = [
  "all",
  "error",
  "warn",
  "info",
  "debug",
] as const;

export type LogSeverity = (typeof logSeverityValues)[number];
export type ConcreteLogSeverity = Exclude<LogSeverity, "all">;

export const logSeverity = (record: LogRecord): ConcreteLogSeverity => {
  const value = record.severityText?.toLowerCase() ?? "";
  const number = record.severityNumber ?? 0;
  if (value.includes("fatal") || value.includes("error") || number >= 17) {
    return "error";
  }
  if (value.includes("warn") || number >= 13) {
    return "warn";
  }
  if (value.includes("info") || number >= 9) {
    return "info";
  }
  if (value.includes("debug") || value.includes("trace") || number > 0) {
    return "debug";
  }
  return "info";
};

export const logSeverityClass: Record<ConcreteLogSeverity, string> = {
  debug: "text-violet-600 dark:text-violet-300",
  error: "text-rose-600 dark:text-rose-300",
  info: "text-sky-600 dark:text-sky-300",
  warn: "text-amber-600 dark:text-amber-300",
};

const severityRank: Record<ConcreteLogSeverity, number> = {
  debug: 0,
  error: 3,
  info: 1,
  warn: 2,
};

export const atLeastLogSeverity = (record: LogRecord, filter: LogSeverity) =>
  filter === "all" || severityRank[logSeverity(record)] >= severityRank[filter];

export const logSeverityOptions = [
  { label: "All levels", value: "all" },
  { label: "Error", value: "error" },
  { label: "Warn and above", value: "warn" },
  { label: "Info and above", value: "info" },
  { label: "Debug and above", value: "debug" },
] as const;
