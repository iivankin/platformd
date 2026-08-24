import {
  debounce,
  parseAsInteger,
  parseAsJson,
  parseAsString,
  parseAsStringLiteral,
} from "nuqs";

import { analyticsFiltersSchema } from "@/analytics-model";
import { logFieldFiltersSchema } from "@/log-field-filter";
import { logSeverityValues } from "@/log-severity";

export const telemetryViewValues = [
  "metrics",
  "logs",
  "errors",
  "traces",
  "settings",
  "ai",
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
  "today",
  "15m",
  "1h",
  "6h",
  "24h",
  "7d",
  "28d",
  "91d",
  "12m",
  "custom",
] as const;

export type TelemetryTimeRange = (typeof telemetryTimeRangeValues)[number];

export const timeRangeQueryParsers = {
  timeFrom: parseAsInteger,
  timeRange: parseAsStringLiteral(telemetryTimeRangeValues).withDefault("all"),
  timeTo: parseAsInteger,
};

export const aiTimeRangeQueryParsers = {
  aiTimeFrom: parseAsInteger,
  aiTimeRange: parseAsStringLiteral(telemetryTimeRangeValues).withDefault(
    "28d"
  ),
  aiTimeTo: parseAsInteger,
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
  logSpan: parseAsString,
  logTrace: parseAsString,
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

export const analyticsPageValues = [
  "dashboard",
  "visitors",
  "heatmaps",
  "retention",
  "ai",
  "flags",
  "charts",
  "settings",
] as const;

export type AnalyticsPage = (typeof analyticsPageValues)[number];

export const analyticsQueryParsers = {
  analyticsFilters: parseAsJson((value) => {
    const parsed = analyticsFiltersSchema.safeParse(value);
    return parsed.success ? parsed.data : null;
  }).withDefault([]),
  analyticsPage:
    parseAsStringLiteral(analyticsPageValues).withDefault("dashboard"),
  timeFrom: parseAsInteger,
  timeRange: parseAsStringLiteral(telemetryTimeRangeValues).withDefault("28d"),
  timeTo: parseAsInteger,
  tracker: parseAsString,
};

export const errorDetailQueryParsers = {
  errorEvent: parseAsString,
  errorIssue: parseAsString,
};

export const scopedErrorQueryParser = parseAsString
  .withDefault("")
  .withOptions({ limitUrlUpdates: debounce(250) });
