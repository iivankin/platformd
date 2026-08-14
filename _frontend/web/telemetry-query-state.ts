import {
  debounce,
  parseAsInteger,
  parseAsJson,
  parseAsString,
  parseAsStringLiteral,
} from "nuqs";

import { logFieldFiltersSchema } from "@/log-field-filter";
import { logSeverityValues } from "@/log-severity";

export const telemetryViewValues = [
  "metrics",
  "logs",
  "errors",
  "traces",
  "settings",
] as const;

export type TelemetryView = (typeof telemetryViewValues)[number];

export const telemetryViewParser =
  parseAsStringLiteral(telemetryViewValues).withDefault("metrics");

export const logSortValues = [
  "timestamp",
  "severity",
  "deploymentId",
  "text",
  "traceId",
] as const;

export type LogSort = (typeof logSortValues)[number];

export const telemetryTimeRangeValues = [
  "all",
  "15m",
  "1h",
  "6h",
  "24h",
  "7d",
  "custom",
] as const;

export type TelemetryTimeRange = (typeof telemetryTimeRangeValues)[number];

export const timeRangeQueryParsers = {
  timeFrom: parseAsInteger,
  timeRange: parseAsStringLiteral(telemetryTimeRangeValues).withDefault("all"),
  timeTo: parseAsInteger,
};

export const logQueryParsers = {
  deployment: parseAsString,
  logFields: parseAsJson((value) => {
    const parsed = logFieldFiltersSchema.safeParse(value);
    return parsed.success ? parsed.data : null;
  }).withDefault([]),
  logLevel: parseAsStringLiteral(logSeverityValues).withDefault("info"),
  logOrder: parseAsStringLiteral(["asc", "desc"] as const).withDefault("desc"),
  logQuery: parseAsString.withDefault(""),
  logSort: parseAsStringLiteral(logSortValues).withDefault("timestamp"),
  ...timeRangeQueryParsers,
};

export const traceQueryParsers = {
  trace: parseAsString,
  traceQuery: parseAsString
    .withDefault("")
    .withOptions({ limitUrlUpdates: debounce(250) }),
  traceSort: parseAsStringLiteral([
    "latest",
    "slowest",
    "spans",
  ] as const).withDefault("latest"),
  traceStatus: parseAsStringLiteral([
    "all",
    "error",
    "ok",
  ] as const).withDefault("all"),
  ...timeRangeQueryParsers,
};
