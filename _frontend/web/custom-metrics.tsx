import { LoaderCircle, RefreshCw, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";

import {
  deleteMetricChart,
  fetchMetricCatalog,
  fetchMetricCharts,
  fetchMetricQuery,
} from "@/api";
import type {
  MetricScope,
  ServiceMetricChart,
  ServiceMetricDescriptor,
  ServiceMetricPoint,
} from "@/api";
import { Button } from "@/components/ui/button";
import { MetricChart } from "@/metric-chart";
import type { MetricPoint, MetricSeries } from "@/metric-chart";
import {
  EditMetricChartButton,
  ServiceMetricChartDialog,
} from "@/service-metric-chart-dialog";
import { metricPalette } from "@/service-metric-model";

const ranges = [
  { duration: 60 * 60_000, label: "1h", value: "1h" },
  { duration: 6 * 60 * 60_000, label: "6h", value: "6h" },
  { duration: 24 * 60 * 60_000, label: "1d", value: "1d" },
  { duration: 7 * 24 * 60 * 60_000, label: "7d", value: "7d" },
  { duration: 30 * 24 * 60 * 60_000, label: "30d", value: "30d" },
] as const;

type MetricRange = (typeof ranges)[number]["value"];

interface CustomMetricPoint extends MetricPoint {
  value: number;
}

interface LoadedMetricSeries {
  label: string;
  points: ServiceMetricPoint[];
}

const formatMetricValue = (value: number, unit: string) => {
  const formatted = new Intl.NumberFormat("en-US", {
    maximumFractionDigits: 2,
    notation: Math.abs(value) >= 10_000 ? "compact" : "standard",
  }).format(value);
  return unit ? `${formatted} ${unit}` : formatted;
};

const scopeDescription = (scope: MetricScope) => {
  if (scope.kind === "installation") {
    return "all services in this installation";
  }
  return scope.kind === "project" ? "services in this project" : "this service";
};

const useStableMetricScope = (scope: MetricScope): MetricScope => {
  const projectID = scope.kind === "installation" ? "" : scope.projectID;
  const serviceID = scope.kind === "service" ? scope.serviceID : "";
  return useMemo(() => {
    if (scope.kind === "installation") {
      return { kind: "installation" };
    }
    if (scope.kind === "project") {
      return { kind: "project", projectID };
    }
    return { kind: "service", projectID, serviceID };
  }, [projectID, scope.kind, serviceID]);
};

const latestPoint = (series: LoadedMetricSeries) => series.points.at(-1);

const ValueVisualization = ({
  actions,
  chart,
  formatValue,
  loading,
  series,
}: {
  actions: ReactNode;
  chart: ServiceMetricChart;
  formatValue: (value: number) => string;
  loading: boolean;
  series: LoadedMetricSeries[];
}) => (
  <div>
    <header className="flex min-h-12 items-center gap-3 border-b border-border px-4 py-2.5">
      <h3 className="mr-auto text-xs font-medium tracking-[0.12em] uppercase">
        {chart.title}
      </h3>
      {actions}
    </header>
    <div className="grid min-h-60 content-center gap-4 px-5 py-8 sm:grid-cols-2">
      {series.length ? (
        series.map((item, index) => {
          const point = latestPoint(item);
          const label =
            series.length > 1 ? item.label : chart.legend || item.label;
          return (
            <div className="border-l border-border pl-4" key={item.label}>
              <p className="flex items-center gap-2 text-[9px] text-muted-foreground">
                <span
                  className="size-1.5"
                  style={{
                    backgroundColor:
                      metricPalette[index % metricPalette.length],
                  }}
                />
                {label}
              </p>
              <p className="mt-2 text-2xl tabular-nums">
                {point ? formatValue(point.value) : "—"}
              </p>
            </div>
          );
        })
      ) : (
        <p className="text-[9px] text-muted-foreground">
          {loading ? "Loading metric samples…" : "No samples in range"}
        </p>
      )}
    </div>
  </div>
);

const CustomMetricChart = ({
  catalog,
  chart,
  onDelete,
  onSaved,
  range,
  revision,
  scope,
}: {
  catalog: ServiceMetricDescriptor[];
  chart: ServiceMetricChart;
  onDelete: (chart: ServiceMetricChart) => Promise<void>;
  onSaved: (chart: ServiceMetricChart) => void;
  range: MetricRange;
  revision: number;
  scope: MetricScope;
}) => {
  const [loadedSeries, setLoadedSeries] = useState<LoadedMetricSeries[]>([]);
  const [bounds, setBounds] = useState({ from: 0, to: 0 });
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const duration =
    ranges.find((candidate) => candidate.value === range)?.duration ??
    ranges[0].duration;

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      setLoading(true);
      const to = Date.now();
      const from = to - duration;
      const step = Math.max(1000, Math.ceil(duration / 180 / 1000) * 1000);
      try {
        const rows = await fetchMetricQuery(
          scope,
          { from, sql: chart.sql, step, to },
          controller.signal
        );
        const grouped = new Map<string, ServiceMetricPoint[]>();
        for (const row of rows) {
          const label = row.series || chart.legend || "value";
          const points = grouped.get(label) ?? [];
          points.push({ timeUnixNano: row.timeUnixNano, value: row.value });
          grouped.set(label, points);
        }
        setBounds({ from, to });
        setLoadedSeries(
          [...grouped].map(([label, points]) => ({
            label,
            points: points.toSorted((left, right) =>
              left.timeUnixNano.localeCompare(right.timeUnixNano)
            ),
          }))
        );
        setError("");
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setLoadedSeries([]);
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load metric series"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [chart, duration, revision, scope]);

  const series = useMemo<MetricSeries<CustomMetricPoint>[]>(
    () =>
      loadedSeries.map((item, index) => ({
        color: metricPalette[index % metricPalette.length] ?? metricPalette[0],
        label:
          loadedSeries.length > 1 ? item.label : chart.legend || item.label,
        points: item.points.map((point) => ({
          observedAt: Number(BigInt(point.timeUnixNano) / 1_000_000n),
          value: point.value,
        })),
        value: (point) => point.value,
      })),
    [chart.legend, loadedSeries]
  );
  const unit = chart.unit ?? "";
  const actions = (
    <div className="flex items-center">
      <EditMetricChartButton
        catalog={catalog}
        chart={chart}
        onSaved={onSaved}
        scope={scope}
      />
      <Button
        aria-label={`Delete ${chart.title}`}
        onClick={() => void onDelete(chart)}
        size="icon"
        variant="ghost"
      >
        <Trash2 />
      </Button>
    </div>
  );

  return (
    <section aria-busy={loading} className="border border-border bg-card">
      <div
        className={`transition-opacity ${loading && loadedSeries.length ? "opacity-45" : "opacity-100"}`}
      >
        {chart.visualization === "value" ? (
          <ValueVisualization
            actions={actions}
            chart={chart}
            formatValue={(value) => formatMetricValue(value, unit)}
            loading={loading}
            series={loadedSeries}
          />
        ) : (
          <MetricChart
            actions={actions}
            emptyLabel={
              error ||
              (loading ? "Loading metric samples…" : "No samples in range")
            }
            formatValue={(value) => formatMetricValue(value, unit)}
            from={bounds.from}
            minimumMaximum={1}
            points={[]}
            series={series}
            title={chart.title}
            to={bounds.to}
            visualization={chart.visualization}
          />
        )}
      </div>
      <div className="flex items-center gap-3 border-t border-border px-4 py-2 text-[8px] text-muted-foreground">
        <p className="min-w-0 flex-1 truncate">
          {chart.sql.replaceAll(/\s+/gu, " ").trim()}
        </p>
        {loading ? (
          <span className="flex shrink-0 items-center gap-1.5">
            <LoaderCircle className="size-3 animate-spin" />
            Loading {range}…
          </span>
        ) : null}
      </div>
    </section>
  );
};

export const CustomMetrics = ({ scope }: { scope: MetricScope }) => {
  const stableScope = useStableMetricScope(scope);
  const [catalog, setCatalog] = useState<ServiceMetricDescriptor[]>([]);
  const [charts, setCharts] = useState<ServiceMetricChart[]>([]);
  const [range, setRange] = useState<MetricRange>("1h");
  const [revision, setRevision] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  const load = useCallback(
    async (signal?: AbortSignal) => {
      const [nextCatalog, nextCharts] = await Promise.all([
        fetchMetricCatalog(stableScope, signal),
        fetchMetricCharts(stableScope, signal),
      ]);
      setCatalog(nextCatalog);
      setCharts(nextCharts);
      setError("");
    },
    [stableScope]
  );

  useEffect(() => {
    const controller = new AbortController();
    const loadMetrics = async () => {
      try {
        await load(controller.signal);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load application metrics"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void loadMetrics();
    return () => controller.abort();
  }, [load]);

  const deleteChart = async (chart: ServiceMetricChart) => {
    try {
      await deleteMetricChart(stableScope, chart.id);
      setCharts((current) => current.filter((item) => item.id !== chart.id));
    } catch (deleteError) {
      setError(
        deleteError instanceof Error
          ? deleteError.message
          : "Unable to delete metric chart"
      );
    }
  };
  const saveChart = (chart: ServiceMetricChart) => {
    setCharts((current) => {
      const index = current.findIndex((item) => item.id === chart.id);
      if (index === -1) {
        return [...current, chart];
      }
      return current.map((item) => (item.id === chart.id ? chart : item));
    });
    setRevision((value) => value + 1);
  };
  return (
    <section>
      <div className="flex flex-wrap items-center gap-2 border-y border-border px-4 py-3">
        <div className="mr-auto">
          <h3 className="text-[10px] font-medium">Custom graphs</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            {loading
              ? "Reading observed metrics…"
              : `${catalog.length.toLocaleString()} metric names across ${scopeDescription(stableScope)}`}
          </p>
        </div>
        {ranges.map((option) => (
          <button
            className={`h-7 border px-2.5 text-[9px] ${
              range === option.value
                ? "border-foreground bg-foreground text-background"
                : "border-border text-muted-foreground hover:bg-muted hover:text-foreground"
            }`}
            key={option.value}
            onClick={() => setRange(option.value)}
            type="button"
          >
            {option.label}
          </button>
        ))}
        <Button
          aria-label="Refresh custom metrics"
          onClick={() => setRevision((value) => value + 1)}
          size="icon"
          variant="ghost"
        >
          {loading ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
        </Button>
        <ServiceMetricChartDialog
          catalog={catalog}
          onSaved={saveChart}
          scope={stableScope}
        />
      </div>
      {error ? (
        <p className="border-x border-b border-destructive/35 bg-destructive/5 px-4 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      {charts.length === 0 ? (
        <div className="grid min-h-44 place-items-center border-b border-border px-6 text-center">
          <div>
            <p className="text-xs font-medium">No custom graphs</p>
            <p className="mt-2 text-[9px] text-muted-foreground">
              Add a graph after the scope reports at least one metric.
            </p>
          </div>
        </div>
      ) : (
        <div className="grid gap-4 p-4 xl:grid-cols-2">
          {charts.map((chart) => (
            <CustomMetricChart
              catalog={catalog}
              chart={chart}
              key={chart.id}
              onDelete={deleteChart}
              onSaved={saveChart}
              range={range}
              revision={revision}
              scope={stableScope}
            />
          ))}
        </div>
      )}
    </section>
  );
};
