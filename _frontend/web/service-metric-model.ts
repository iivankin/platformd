import type { ServiceMetricChart, ServiceMetricDescriptor } from "@/api";

export type ServiceMetricChartDraft = Omit<
  ServiceMetricChart,
  "createdAt" | "id" | "updatedAt"
>;

const sqlString = (value: string) => value.replaceAll("'", "''");

export const metricQueryTemplates = (
  descriptor?: ServiceMetricDescriptor
): { label: string; sql: string }[] => {
  const metric = sqlString(descriptor?.name ?? "http.server.request.duration");
  return [
    {
      label: "Average",
      sql: `SELECT bucket AS time, avg(value) AS value
FROM metrics
WHERE name = '${metric}'
GROUP BY bucket
ORDER BY time`,
    },
    {
      label: "Counter rate",
      sql: `SELECT bucket AS time,
  sum(delta) / min(step_seconds) AS value
FROM metrics
WHERE name = '${metric}'
GROUP BY bucket
ORDER BY time`,
    },
    {
      label: "Ratio",
      sql: `SELECT bucket AS time,
  100 * sumIf(delta, name = 'errors_total') /
    nullIf(sumIf(delta, name = 'requests_total'), 0) AS value
FROM metrics
WHERE name IN ('errors_total', 'requests_total')
GROUP BY bucket
ORDER BY time`,
    },
    {
      label: "Group by attribute",
      sql: `SELECT bucket AS time, avg(value) AS value,
  attributes['region'] AS series
FROM metrics
WHERE name = '${metric}'
GROUP BY bucket, series
ORDER BY time, series`,
    },
    {
      label: "Histogram p95",
      sql: `SELECT bucket AS time,
  quantileExactWeighted(0.95)(
    tupleElement(sample, 1), tupleElement(sample, 2)
  ) AS value
FROM (
  SELECT bucket,
    arrayJoin(arrayZip(histogram_values, histogram_delta_counts)) AS sample
  FROM metrics
  WHERE name = '${metric}'
)
GROUP BY bucket
ORDER BY time`,
    },
  ];
};

export const metricSqlColumns = [
  ["service_id", "String", "Platformd service identifier"],
  ["timestamp", "UInt64", "Original point time in nanoseconds"],
  ["bucket", "UInt64", "Chart bucket time in nanoseconds"],
  ["step_seconds", "Float64", "Selected chart resolution"],
  ["name", "String", "OTel metric name"],
  ["description", "String", "OTel description"],
  ["unit", "String", "OTel unit"],
  ["kind", "String", "gauge, sum, histogram, exponential_histogram, summary"],
  ["attributes", "Map(String, String)", "Point attributes"],
  ["value", "Nullable(Float64)", "Gauge or sum point value"],
  ["delta", "Nullable(Float64)", "Reset-safe delta for sum metrics"],
  ["count", "Nullable(UInt64)", "Histogram or summary count"],
  ["count_delta", "Nullable(UInt64)", "Reset-safe count delta"],
  ["sum", "Nullable(Float64)", "Histogram or summary sum"],
  ["sum_delta", "Nullable(Float64)", "Reset-safe sum delta"],
  ["min", "Nullable(Float64)", "Point minimum"],
  ["max", "Nullable(Float64)", "Point maximum"],
  ["histogram_values", "Array(Float64)", "Representative bucket values"],
  ["histogram_counts", "Array(UInt64)", "Raw bucket counts"],
  ["histogram_delta_counts", "Array(UInt64)", "Reset-safe bucket count deltas"],
] as const;

export const emptyMetricChartDraft = (
  descriptor?: ServiceMetricDescriptor
): ServiceMetricChartDraft => ({
  legend: "",
  sql: metricQueryTemplates(descriptor)[0]?.sql ?? "",
  title: descriptor?.name ?? "",
  unit: descriptor?.unit || undefined,
  visualization: "area",
});

export const metricPalette = [
  "#38bdf8",
  "#34d399",
  "#fbbf24",
  "#fb7185",
  "#a78bfa",
  "#22d3ee",
  "#f97316",
  "#a3e635",
] as const;
