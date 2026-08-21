import { LoaderCircle, Plus, Trash2, X } from "lucide-react";
import { useQueryStates } from "nuqs";
import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";

import {
  AnalyticsChartDialog,
  AnalyticsFilterChips,
  AnalyticsFilterPopover,
  ExperimentDialog,
  FlagDialog,
  FunnelDialog,
  GoalDialog,
  TrackerDialog,
} from "@/analytics-dialogs";
import {
  analyticsCountryName,
  analyticsLocationName,
} from "@/analytics-location";
import {
  analyticsBotPurpose,
  analyticsCookieDomain,
  analyticsFlagSummary,
  asNumber,
  asString,
  deltaTone,
  firstRecord,
  formatCount,
  formatDelta,
  formatDuration,
  formatPercent,
  funnelReached,
  isAbortError,
  recordRows,
  retentionDays,
  viewportBuckets,
  wilsonInterval,
} from "@/analytics-model";
import { AnalyticsSetupGuide } from "@/analytics-setup";
import { AnalyticsWorldMap } from "@/analytics-world-map";
import {
  deleteAnalyticsChart,
  deleteAnalyticsFlag,
  deleteAnalyticsFunnel,
  deleteAnalyticsGoal,
  deleteAnalyticsTracker,
  fetchAnalyticsCharts,
  fetchAnalyticsExperiments,
  fetchAnalyticsFlags,
  fetchAnalyticsFunnels,
  fetchAnalyticsGoals,
  fetchAnalyticsTrackers,
  fetchServiceDomains,
  queryAnalytics,
  shipAnalyticsExperiment,
  stopAnalyticsExperiment,
} from "@/api";
import type {
  AnalyticsChart,
  AnalyticsExperiment,
  AnalyticsFilter,
  AnalyticsFlag,
  AnalyticsFunnel,
  AnalyticsGoal,
  AnalyticsTracker,
} from "@/api";
import { Button } from "@/components/ui/button";
import { FieldSelect } from "@/field-select";
import { cn } from "@/lib/utils";
import { MetricChart } from "@/metric-chart";
import { metricPalette } from "@/service-metric-model";
import { HighlightedSnippet } from "@/snippet-code";
import { analyticsQueryParsers } from "@/telemetry-query-state";
import type { AnalyticsPage } from "@/telemetry-query-state";
import {
  TelemetryTimeRangePicker,
  telemetryTimeBounds,
} from "@/telemetry-time-range";

/* Page sections sit below the workspace shell. */
/* eslint-disable complexity, no-use-before-define */
/* eslint-disable promise/prefer-await-to-then */

const navItems: { id: AnalyticsPage; label: string }[] = [
  { id: "dashboard", label: "Dashboard" },
  { id: "visitors", label: "Visitors" },
  { id: "heatmaps", label: "Heatmaps" },
  { id: "retention", label: "Retention" },
  { id: "ai", label: "AI" },
  { id: "flags", label: "Flags" },
  { id: "charts", label: "Charts" },
  { id: "settings", label: "Settings" },
];

interface QueryPoint {
  bounceRate: number;
  duration: number;
  observedAt: number;
  pageviews: number;
  visitors: number;
  visits: number;
}

type OverviewSeries =
  | "bounce"
  | "duration"
  | "pageviews"
  | "views_per_visit"
  | "visits"
  | "visitors";

const overviewValue = (point: QueryPoint, series: OverviewSeries) => {
  if (series === "bounce") {
    return point.bounceRate;
  }
  if (series === "duration") {
    return point.duration;
  }
  if (series === "views_per_visit") {
    return point.visits === 0 ? 0 : point.pageviews / point.visits;
  }
  return point[series];
};

const overviewFormat = (series: OverviewSeries, value: number) => {
  if (series === "bounce") {
    return formatPercent(value);
  }
  if (series === "duration") {
    return formatDuration(value);
  }
  if (series === "views_per_visit") {
    return value.toFixed(2);
  }
  return formatCount(value);
};

const useAnalyticsQuery = (
  projectID: string,
  trackerID: string | undefined,
  query: Parameters<typeof queryAnalytics>[2] | undefined
) => {
  const [data, setData] = useState<unknown>();
  const [error, setError] = useState("");
  const encoded = JSON.stringify(query ?? null);

  useEffect(() => {
    if (!trackerID || !query) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const payload = await queryAnalytics(
          projectID,
          trackerID,
          query,
          controller.signal
        );
        setData(payload);
        setError("");
      } catch (loadError: unknown) {
        if (isAbortError(loadError)) {
          return;
        }
        setData(undefined);
        setError(
          loadError instanceof Error ? loadError.message : "Query failed"
        );
      }
    };
    void load();
    return () => controller.abort();
    // query is serialized into encoded so identity changes do not refetch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [encoded, projectID, trackerID]);

  return { data, error };
};

const actionMessage = (error: unknown) =>
  error instanceof Error ? error.message : "Action failed";

const QueryError = ({
  className,
  error,
}: {
  className?: string;
  error: string;
}) =>
  error ? (
    <p className={cn("px-3 py-2 text-[10px] text-destructive", className)}>
      {error}
    </p>
  ) : null;

export const ProjectAnalytics = ({
  projectID,
  serviceIDs,
}: {
  projectID: string;
  serviceIDs: string[];
}) => {
  const [query, setQuery] = useQueryStates(analyticsQueryParsers);
  const [trackers, setTrackers] = useState<AnalyticsTracker[]>();
  const [hostnames, setHostnames] = useState<string[]>([]);
  const [error, setError] = useState("");
  const tracker =
    trackers?.find((entry) => entry.id === query.tracker) ?? trackers?.[0];

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [nextTrackers, domainLists] = await Promise.all([
          fetchAnalyticsTrackers(projectID, controller.signal),
          Promise.all(
            serviceIDs.map((serviceID) =>
              fetchServiceDomains(projectID, serviceID, controller.signal)
            )
          ),
        ]);
        setTrackers(nextTrackers);
        setHostnames(
          [
            ...new Set(domainLists.flat().map((domain) => domain.hostname)),
          ].toSorted()
        );
        setError("");
        if (!query.tracker && nextTrackers[0]) {
          void setQuery({ tracker: nextTrackers[0].id });
        }
      } catch (loadError) {
        if (!isAbortError(loadError)) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load analytics"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
    // Selecting the default tracker updates the URL; including it here would
    // abort this load and every dashboard query on first paint.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [projectID, serviceIDs, setQuery]);

  const addFilter = useCallback(
    (filter: AnalyticsFilter) => {
      if (query.analyticsFilters.length >= 8) {
        return;
      }
      void setQuery({
        analyticsFilters: [...query.analyticsFilters, filter],
      });
    },
    [query.analyticsFilters, setQuery]
  );

  if (error) {
    return <p className="px-5 py-6 text-[10px] text-destructive">{error}</p>;
  }
  if (!trackers) {
    return (
      <div className="grid min-h-72 place-items-center text-[10px] text-muted-foreground">
        <span className="flex items-center gap-2">
          <LoaderCircle className="size-3 animate-spin" /> Loading analytics
        </span>
      </div>
    );
  }
  if (trackers.length === 0 || !tracker) {
    return (
      <EmptyTrackers
        hostnames={hostnames}
        onSaved={(created) => {
          setTrackers([created]);
          void setQuery({ tracker: created.id });
        }}
        projectID={projectID}
      />
    );
  }

  const bounds = telemetryTimeBounds({
    from: query.timeFrom,
    range: query.timeRange,
    to: query.timeTo,
  });
  const page = query.analyticsPage;

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <header className="sticky top-0 z-20 flex min-h-12 flex-wrap items-center gap-2 border-b border-border bg-background px-3 py-2">
        <FieldSelect
          aria-label="Tracker"
          className="w-fit min-w-40"
          items={trackers.map((entry) => ({
            label: entry.rootDomain,
            value: entry.id,
          }))}
          onValueChange={(next) => void setQuery({ tracker: next })}
          value={tracker.id}
        />
        <TrackerDialog
          hostnames={hostnames}
          onSaved={(created) => {
            setTrackers((current) => [...(current ?? []), created]);
            void setQuery({ tracker: created.id });
          }}
          projectID={projectID}
          trackers={trackers}
          trigger={
            <Button size="sm" variant="ghost">
              <Plus /> Tracker
            </Button>
          }
        />
        {page === "dashboard" ? (
          <RealtimeBadge projectID={projectID} trackerID={tracker.id} />
        ) : null}
        <div className="min-w-0 flex-1">
          <AnalyticsFilterChips
            filters={query.analyticsFilters}
            onRemove={(index) =>
              void setQuery({
                analyticsFilters: query.analyticsFilters.filter(
                  (_, entryIndex) => entryIndex !== index
                ),
              })
            }
          />
        </div>
        <AnalyticsFilterPopover
          filters={query.analyticsFilters}
          onChange={(filters) => void setQuery({ analyticsFilters: filters })}
          projectID={projectID}
          trackerID={tracker.id}
        />
        <TelemetryTimeRangePicker
          onChange={(value) =>
            void setQuery({
              timeFrom: value.from,
              timeRange: value.range,
              timeTo: value.to,
            })
          }
          value={{
            from: query.timeFrom,
            range: query.timeRange,
            to: query.timeTo,
          }}
        />
      </header>
      <nav
        aria-label="Analytics pages"
        className="flex h-10 shrink-0 items-stretch overflow-x-auto border-b border-border px-3"
      >
        {navItems.map((item) => (
          <button
            className={cn(
              "relative shrink-0 px-3 text-[9px] tracking-[0.1em] text-muted-foreground uppercase after:absolute after:right-3 after:bottom-0 after:left-3 after:h-px after:bg-transparent hover:text-foreground",
              item.id === page && "text-foreground after:bg-foreground"
            )}
            key={item.id}
            onClick={() => void setQuery({ analyticsPage: item.id })}
            type="button"
          >
            {item.label}
          </button>
        ))}
      </nav>
      <div className="min-h-0 flex-1">
        {page === "dashboard" ? (
          <DashboardPage
            filters={query.analyticsFilters}
            from={bounds.from}
            hostnames={tracker.matchingHostnames}
            key={tracker.id}
            onFilter={addFilter}
            projectID={projectID}
            to={bounds.to}
            tracker={tracker}
          />
        ) : null}
        {page === "visitors" ? (
          <VisitorsPage
            filters={query.analyticsFilters}
            from={bounds.from}
            key={tracker.id}
            projectID={projectID}
            to={bounds.to}
            tracker={tracker}
          />
        ) : null}
        {page === "heatmaps" ? (
          <HeatmapsPage
            filters={query.analyticsFilters}
            from={bounds.from}
            key={tracker.id}
            projectID={projectID}
            to={bounds.to}
            tracker={tracker}
          />
        ) : null}
        {page === "retention" ? (
          <RetentionPage
            filters={query.analyticsFilters}
            from={bounds.from}
            key={tracker.id}
            projectID={projectID}
            to={bounds.to}
            tracker={tracker}
          />
        ) : null}
        {page === "ai" ? (
          <AIPage
            filters={query.analyticsFilters}
            from={bounds.from}
            key={tracker.id}
            onFilter={addFilter}
            projectID={projectID}
            to={bounds.to}
            tracker={tracker}
          />
        ) : null}
        {page === "flags" ? (
          <FlagsPage
            from={bounds.from}
            key={tracker.id}
            projectID={projectID}
            to={bounds.to}
            tracker={tracker}
          />
        ) : null}
        {page === "charts" ? (
          <ChartsPage
            from={bounds.from}
            key={tracker.id}
            projectID={projectID}
            to={bounds.to}
            tracker={tracker}
          />
        ) : null}
        {page === "settings" ? (
          <SettingsPage
            hostnames={hostnames}
            key={tracker.id}
            onDeleted={() => {
              setTrackers((current) =>
                (current ?? []).filter((entry) => entry.id !== tracker.id)
              );
              void setQuery({ tracker: null });
            }}
            onSaved={(saved) =>
              setTrackers((current) =>
                (current ?? []).map((entry) =>
                  entry.id === saved.id ? saved : entry
                )
              )
            }
            projectID={projectID}
            tracker={tracker}
            trackers={trackers}
          />
        ) : null}
      </div>
    </div>
  );
};

const EmptyTrackers = ({
  hostnames,
  onSaved,
  projectID,
}: {
  hostnames: string[];
  onSaved: (tracker: AnalyticsTracker) => void;
  projectID: string;
}) => (
  <div className="px-5 py-8">
    <div className="flex items-start justify-between gap-3">
      <div>
        <h2 className="text-sm font-medium">Web analytics trackers</h2>
        <p className="mt-1.5 max-w-xl text-[10px] leading-4 text-muted-foreground">
          Create a tracker for a root domain. Hostnames under that root share
          visitors. Service tabs stay Metrics, Logs, Errors, Traces, and
          Settings.
        </p>
      </div>
      <TrackerDialog
        hostnames={hostnames}
        onSaved={onSaved}
        projectID={projectID}
        trackers={[]}
        trigger={
          <Button>
            <Plus /> Create tracker
          </Button>
        }
      />
    </div>
    <div className="mt-6 border border-border">
      <div className="grid grid-cols-3 border-b border-border px-3 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
        <span>Name</span>
        <span>Root</span>
        <span>Hostnames</span>
      </div>
      <p className="px-3 py-8 text-center text-[10px] text-muted-foreground">
        No trackers yet.
      </p>
    </div>
  </div>
);

const RealtimeBadge = ({
  projectID,
  trackerID,
}: {
  projectID: string;
  trackerID: string;
}) => {
  const [visitors, setVisitors] = useState<number>();
  useEffect(() => {
    let cancelled = false;
    const load = async () => {
      try {
        const payload = await queryAnalytics(projectID, trackerID, {
          report: "realtime",
        });
        if (!cancelled) {
          setVisitors(asNumber(firstRecord(payload).visitors));
        }
      } catch {
        // Realtime is best-effort; keep the last known count.
      }
    };
    void load();
    const timer = setInterval(() => void load(), 30_000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [projectID, trackerID]);
  if (visitors === undefined) {
    return null;
  }
  return (
    <span className="text-[10px] text-muted-foreground">
      {formatCount(visitors)} current visitors
    </span>
  );
};

const DashboardPage = ({
  filters,
  from,
  hostnames,
  onFilter,
  projectID,
  to,
  tracker,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  hostnames: string[];
  onFilter: (filter: AnalyticsFilter) => void;
  projectID: string;
  to?: number;
  tracker: AnalyticsTracker;
}) => {
  const overview = useAnalyticsQuery(projectID, tracker.id, {
    filters,
    from,
    report: "overview",
    to,
  });
  const current = firstRecord(
    overview.data && typeof overview.data === "object"
      ? (overview.data as { current?: unknown }).current
      : undefined
  );
  const previous = firstRecord(
    overview.data && typeof overview.data === "object"
      ? (overview.data as { previous?: unknown }).previous
      : undefined
  );
  const timeseries = recordRows(
    overview.data && typeof overview.data === "object"
      ? (overview.data as { timeseries?: unknown }).timeseries
      : undefined
  ).map((row) => ({
    bounceRate: asNumber(row.bounce_rate),
    duration: asNumber(row.duration),
    observedAt: asNumber(row.time),
    pageviews: asNumber(row.pageviews),
    visitors: asNumber(row.visitors),
    visits: asNumber(row.visits),
  }));
  const visitors = asNumber(current.visitors);
  const visits = asNumber(current.visits);
  const pageviews = asNumber(current.pageviews);
  const bounce = asNumber(current.bounce_rate);
  const duration = asNumber(current.duration);
  const viewsPerVisit = visits === 0 ? 0 : pageviews / visits;
  const previousVisits = asNumber(previous.visits);
  const previousPageviews = asNumber(previous.pageviews);
  const previousViewsPerVisit =
    previousVisits === 0 ? 0 : previousPageviews / previousVisits;
  const [series, setSeries] = useState<OverviewSeries>("visitors");
  const kpis: {
    delta: number;
    id: OverviewSeries;
    label: string;
    value: string;
  }[] = [
    {
      delta: formatDelta(visitors, asNumber(previous.visitors)),
      id: "visitors",
      label: "Unique visitors",
      value: formatCount(visitors),
    },
    {
      delta: formatDelta(visits, previousVisits),
      id: "visits",
      label: "Total visits",
      value: formatCount(visits),
    },
    {
      delta: formatDelta(pageviews, previousPageviews),
      id: "pageviews",
      label: "Total pageviews",
      value: formatCount(pageviews),
    },
    {
      delta: formatDelta(viewsPerVisit, previousViewsPerVisit),
      id: "views_per_visit",
      label: "Views per visit",
      value: viewsPerVisit.toFixed(2),
    },
    {
      delta: formatDelta(bounce, asNumber(previous.bounce_rate), true),
      id: "bounce",
      label: "Bounce rate",
      value: formatPercent(bounce),
    },
    {
      delta: formatDelta(duration, asNumber(previous.duration)),
      id: "duration",
      label: "Visit duration",
      value: formatDuration(duration),
    },
  ];
  const selectedKpi = kpis.find((kpi) => kpi.id === series) ?? kpis[0];

  return (
    <div className="flex flex-col">
      <QueryError error={overview.error} />
      <section className="border-b border-border px-5 py-5">
        <div className="grid grid-cols-2 gap-px border border-border sm:grid-cols-3 lg:grid-cols-6">
          {kpis.map((kpi) => (
            <button
              className={cn(
                "bg-background px-3 py-3 text-left hover:bg-muted/40",
                series === kpi.id && "bg-muted/55"
              )}
              key={kpi.label}
              onClick={() => setSeries(kpi.id)}
              type="button"
            >
              <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                {kpi.label}
              </p>
              <p className="mt-1 text-sm">{kpi.value}</p>
              <p className={cn("mt-1 text-[10px]", deltaTone(kpi.delta))}>
                {kpi.delta === 0
                  ? "—"
                  : `${kpi.delta > 0 ? "+" : ""}${Math.round(kpi.delta * 100)}%`}
              </p>
            </button>
          ))}
        </div>
        <div className="mt-4">
          <MetricChart<QueryPoint>
            emptyLabel="No analytics in this range"
            formatValue={(value) => overviewFormat(series, value)}
            from={from ?? timeseries[0]?.observedAt ?? 0}
            minimumMaximum={series === "bounce" ? 0.05 : 5}
            points={timeseries}
            series={[
              {
                color: metricPalette[0],
                label: selectedKpi?.label ?? series,
                value: (point) => overviewValue(point, series),
              },
            ]}
            title=""
            to={to ?? timeseries.at(-1)?.observedAt ?? 0}
            visualization="area"
          />
        </div>
      </section>
      <div className="grid border-b border-border lg:grid-cols-2">
        <BreakdownPanel
          filters={filters}
          from={from}
          onFilter={onFilter}
          projectID={projectID}
          tabs={[
            { dimension: "source", label: "Sources" },
            { dimension: "channel", label: "Channels" },
            { dimension: "utm_campaign", label: "Campaigns" },
          ]}
          to={to}
          trackerID={tracker.id}
        />
        <BreakdownPanel
          filters={filters}
          from={from}
          onFilter={onFilter}
          projectID={projectID}
          tabs={[
            { dimension: "page", label: "Top pages" },
            { dimension: "entry", label: "Entry" },
            { dimension: "exit", label: "Exit" },
          ]}
          to={to}
          trackerID={tracker.id}
        />
      </div>
      <div className="grid border-b border-border lg:grid-cols-2">
        <LocationsPanel
          filters={filters}
          from={from}
          onFilter={onFilter}
          projectID={projectID}
          to={to}
          trackerID={tracker.id}
        />
        <BreakdownPanel
          filters={filters}
          from={from}
          onFilter={onFilter}
          projectID={projectID}
          tabs={[
            { dimension: "browser", label: "Browsers" },
            { dimension: "os", label: "OS" },
            { dimension: "device", label: "Devices" },
          ]}
          to={to}
          trackerID={tracker.id}
        />
      </div>
      <BehavioursPanel
        filters={filters}
        from={from}
        hostnames={hostnames}
        onFilter={onFilter}
        projectID={projectID}
        to={to}
        tracker={tracker}
      />
    </div>
  );
};

const BreakdownPanel = ({
  filters,
  from,
  onFilter,
  projectID,
  tabs,
  to,
  trackerID,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  onFilter: (filter: AnalyticsFilter) => void;
  projectID: string;
  tabs: { dimension: string; label: string }[];
  to?: number;
  trackerID: string;
}) => {
  const [tab, setTab] = useState(tabs[0]?.dimension ?? "source");
  const query = useAnalyticsQuery(projectID, trackerID, {
    dimension: tab,
    filters,
    from,
    report: "breakdown",
    to,
  });
  const rows = recordRows(query.data).map((row) => ({
    events: asNumber(row.events),
    filterValue: typeof row.label === "string" ? row.label : "",
    label: asString(row.label, "(none)"),
    visitors: asNumber(row.visitors),
  }));
  const maximum = Math.max(1, ...rows.map((row) => row.visitors));
  let filterDimension = tab;
  if (tab === "entry" || tab === "entry_page") {
    filterDimension = "entry_page";
  } else if (tab === "exit" || tab === "exit_page") {
    filterDimension = "exit_page";
  }

  return (
    <section className="min-h-[27rem] border-r border-border last:border-r-0">
      <div className="flex h-10 items-center gap-1 border-b border-border px-3">
        {tabs.map((entry) => (
          <button
            className={cn(
              "px-2 text-[9px] tracking-[0.1em] text-muted-foreground uppercase",
              tab === entry.dimension && "text-foreground"
            )}
            key={entry.dimension}
            onClick={() => setTab(entry.dimension)}
            type="button"
          >
            {entry.label}
          </button>
        ))}
      </div>
      <QueryError error={query.error} />
      <div className="px-3 py-2">
        {rows.length === 0 ? (
          <p className="py-8 text-center text-[10px] text-muted-foreground">
            No data
          </p>
        ) : (
          rows.slice(0, 9).map((row) => (
            <button
              className="group flex h-[30px] w-full items-center gap-2 text-left text-[10px]"
              key={row.label}
              onClick={() => {
                if (!row.filterValue) {
                  return;
                }
                onFilter({
                  dimension: filterDimension,
                  operator: "is",
                  value: row.filterValue,
                });
              }}
              type="button"
            >
              <span className="relative min-w-0 flex-1 truncate px-1">
                <span
                  className="absolute inset-y-0.5 left-0 bg-muted"
                  style={{ width: `${(row.visitors / maximum) * 100}%` }}
                />
                <span className="relative">{row.label}</span>
              </span>
              <span className="w-12 text-right">
                {formatCount(row.visitors)}
              </span>
              <span className="w-10 text-right text-muted-foreground opacity-0 group-hover:opacity-100">
                {formatPercent(row.visitors / maximum)}
              </span>
            </button>
          ))
        )}
      </div>
    </section>
  );
};

const LocationsPanel = ({
  filters,
  from,
  onFilter,
  projectID,
  to,
  trackerID,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  onFilter: (filter: AnalyticsFilter) => void;
  projectID: string;
  to?: number;
  trackerID: string;
}) => {
  const [tab, setTab] = useState("map");
  let dimension = "city";
  if (tab === "map" || tab === "country") {
    dimension = "country";
  } else if (tab === "region") {
    dimension = "region";
  }
  const query = useAnalyticsQuery(projectID, trackerID, {
    dimension,
    filters,
    from,
    report: "breakdown",
    to,
  });
  const rows = recordRows(query.data).map((row) => ({
    label: asString(row.label, "(none)"),
    visitors: asNumber(row.visitors),
  }));
  return (
    <section className="flex min-h-[27rem] flex-col border-r border-border">
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-border px-3">
        {(
          [
            ["map", "Map"],
            ["country", "Countries"],
            ["region", "Regions"],
            ["city", "Cities"],
          ] as const
        ).map(([id, label]) => (
          <button
            className={cn(
              "px-2 text-[9px] tracking-[0.1em] text-muted-foreground uppercase",
              tab === id && "text-foreground"
            )}
            key={id}
            onClick={() => setTab(id)}
            type="button"
          >
            {label}
          </button>
        ))}
      </div>
      <QueryError error={query.error} />
      {tab === "map" ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="px-3 pt-3">
            <AnalyticsWorldMap
              onSelect={(country) =>
                onFilter({
                  dimension: "country",
                  operator: "is",
                  value: country,
                })
              }
              rows={rows}
            />
          </div>
          <div className="min-h-0 flex-1 overflow-auto px-3 py-2">
            {rows.slice(0, 8).map((row) => (
              <button
                className="flex h-[30px] w-full items-center justify-between text-[10px]"
                key={row.label}
                onClick={() =>
                  onFilter({
                    dimension: "country",
                    operator: "is",
                    value: row.label,
                  })
                }
                type="button"
              >
                <span>{analyticsCountryName(row.label)}</span>
                <span>{formatCount(row.visitors)}</span>
              </button>
            ))}
          </div>
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto px-3 py-2">
          {rows.slice(0, 9).map((row) => (
            <button
              className="flex h-[30px] w-full items-center justify-between text-[10px]"
              key={row.label}
              onClick={() =>
                onFilter({ dimension, operator: "is", value: row.label })
              }
              type="button"
            >
              <span>
                {tab === "country"
                  ? analyticsCountryName(row.label)
                  : row.label}
              </span>
              <span>{formatCount(row.visitors)}</span>
            </button>
          ))}
        </div>
      )}
    </section>
  );
};

const GoalRow = ({
  filters,
  from,
  goal,
  onDeleted,
  onError,
  onFilter,
  projectID,
  to,
  trackerID,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  goal: AnalyticsGoal;
  onDeleted: () => void;
  onError: (message: string) => void;
  onFilter: (filter: AnalyticsFilter) => void;
  projectID: string;
  to?: number;
  trackerID: string;
}) => {
  const actionFilter: AnalyticsFilter = {
    dimension: goal.actionType === "path" ? "page" : "event",
    operator: "is",
    value: goal.actionValue,
  };
  const extra: AnalyticsFilter[] = [actionFilter];
  if (goal.hostname) {
    extra.push({
      dimension: "hostname",
      operator: "is",
      value: goal.hostname,
    });
  }
  const query = useAnalyticsQuery(projectID, trackerID, {
    dimension: actionFilter.dimension,
    filters: [...filters, ...extra],
    from,
    report: "breakdown",
    to,
  });
  const [match] = recordRows(query.data);
  return (
    <div className="grid grid-cols-4 items-center border-b border-border py-2 text-[10px]">
      <button
        className="text-left"
        onClick={() => onFilter(actionFilter)}
        type="button"
      >
        {goal.name}
      </button>
      <span>{query.error ? "—" : formatCount(asNumber(match?.visitors))}</span>
      <span>{query.error ? "—" : formatCount(asNumber(match?.events))}</span>
      <span className="text-right">
        <Button
          onClick={() => {
            void (async () => {
              try {
                await deleteAnalyticsGoal(projectID, trackerID, goal.id);
                onDeleted();
              } catch (error: unknown) {
                onError(actionMessage(error));
              }
            })();
          }}
          size="sm"
          type="button"
          variant="ghost"
        >
          <Trash2 />
        </Button>
      </span>
    </div>
  );
};

const BehavioursPanel = ({
  filters,
  from,
  hostnames,
  onFilter,
  projectID,
  to,
  tracker,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  hostnames: string[];
  onFilter: (filter: AnalyticsFilter) => void;
  projectID: string;
  to?: number;
  tracker: AnalyticsTracker;
}) => {
  const [tab, setTab] = useState<"goals" | "events" | "funnels" | "explore">(
    "goals"
  );
  const [goals, setGoals] = useState<AnalyticsGoal[]>([]);
  const [funnels, setFunnels] = useState<AnalyticsFunnel[]>([]);
  const [funnelID, setFunnelID] = useState("");
  const [listError, setListError] = useState("");
  const [actionError, setActionError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [nextGoals, nextFunnels] = await Promise.all([
          fetchAnalyticsGoals(projectID, tracker.id, controller.signal),
          fetchAnalyticsFunnels(projectID, tracker.id, controller.signal),
        ]);
        setGoals(nextGoals);
        setFunnels(nextFunnels);
        setFunnelID(nextFunnels[0]?.id ?? "");
        setListError("");
      } catch (loadError) {
        if (!isAbortError(loadError)) {
          setListError(actionMessage(loadError));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, tracker.id]);
  const events = useAnalyticsQuery(projectID, tracker.id, {
    dimension: "event",
    filters,
    from,
    report: "breakdown",
    to,
  });
  const pages = useAnalyticsQuery(projectID, tracker.id, {
    dimension: "page",
    filters,
    from,
    report: "breakdown",
    to,
  });
  const funnelQuery = useAnalyticsQuery(
    projectID,
    tracker.id,
    tab === "funnels" && funnelID
      ? { filters, from, funnelId: funnelID, report: "funnel", to }
      : undefined
  );
  const paths = useAnalyticsQuery(
    projectID,
    tracker.id,
    tab === "explore" ? { filters, from, report: "paths", to } : undefined
  );
  const selectedFunnel = funnels.find((funnel) => funnel.id === funnelID);
  const funnelRows = funnelReached(
    recordRows(funnelQuery.data).map((row) => ({
      level: asNumber(row.level),
      visitors: asNumber(row.visitors),
    })),
    selectedFunnel?.steps.length ?? 0
  );

  return (
    <section className="px-5 py-5">
      <div className="flex flex-wrap items-center gap-2">
        {(
          [
            ["goals", "Goals"],
            ["events", "Events"],
            ["funnels", "Funnels"],
            ["explore", "Explore"],
          ] as const
        ).map(([id, label]) => (
          <button
            className={cn(
              "px-2 text-[9px] tracking-[0.1em] text-muted-foreground uppercase",
              tab === id && "text-foreground"
            )}
            key={id}
            onClick={() => setTab(id)}
            type="button"
          >
            {label}
          </button>
        ))}
        <div className="ml-auto flex gap-2">
          <GoalDialog
            hostnames={hostnames}
            onSaved={(goal) => setGoals((current) => [...current, goal])}
            projectID={projectID}
            trackerID={tracker.id}
            trigger={
              <Button size="sm" variant="ghost">
                <Plus /> Goal
              </Button>
            }
          />
          <FunnelDialog
            hostnames={hostnames}
            onSaved={(funnel) => {
              setFunnels((current) => [...current, funnel]);
              setFunnelID(funnel.id);
            }}
            projectID={projectID}
            trackerID={tracker.id}
            trigger={
              <Button size="sm" variant="ghost">
                <Plus /> Funnel
              </Button>
            }
          />
        </div>
      </div>
      {tab === "goals" ? (
        <div className="mt-4">
          <QueryError
            error={events.error || pages.error || listError || actionError}
          />
          <div className="grid grid-cols-4 border-b border-border py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            <span>Goal</span>
            <span>Uniques</span>
            <span>Total</span>
            <span />
          </div>
          {goals.length === 0 ? (
            <p className="py-8 text-center text-[10px] text-muted-foreground">
              {listError ? "Unable to load goals." : "No goals yet."}
            </p>
          ) : (
            goals.map((goal) => (
              <GoalRow
                filters={filters}
                from={from}
                goal={goal}
                key={goal.id}
                onDeleted={() =>
                  setGoals((current) =>
                    current.filter((entry) => entry.id !== goal.id)
                  )
                }
                onError={setActionError}
                onFilter={onFilter}
                projectID={projectID}
                to={to}
                trackerID={tracker.id}
              />
            ))
          )}
        </div>
      ) : null}
      {tab === "events" ? (
        <>
          <QueryError error={events.error} />
          <BreakdownList
            onFilter={(label) =>
              onFilter({ dimension: "event", operator: "is", value: label })
            }
            rows={recordRows(events.data).map((row) => ({
              label: asString(row.label),
              visitors: asNumber(row.visitors),
            }))}
          />
        </>
      ) : null}
      {tab === "funnels" ? (
        <div className="mt-4">
          <QueryError error={funnelQuery.error || actionError} />
          {funnels.length === 0 ? (
            <p className="text-[10px] text-muted-foreground">
              Create a funnel to start.
            </p>
          ) : (
            <FieldSelect
              aria-label="Funnel"
              className="w-fit min-w-48"
              items={funnels.map((funnel) => ({
                label: funnel.name,
                value: funnel.id,
              }))}
              onValueChange={setFunnelID}
              value={funnelID}
            />
          )}
          <div className="mt-4 grid gap-3">
            {(selectedFunnel?.steps ?? []).map((step, index) => {
              const current = funnelRows[index];
              const previous = funnelRows[index - 1] ?? current;
              const drop =
                previous && current && previous.reached > 0
                  ? 1 - current.reached / previous.reached
                  : 0;
              return (
                <div
                  className="flex items-center gap-3"
                  key={`${step.type}:${step.value}:${index}`}
                >
                  <span className="grid size-10 place-items-center border border-border text-xs">
                    {index + 1}
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="text-[10px]">
                      {step.hostname
                        ? `${step.type} · ${step.hostname}${step.value}`
                        : `${step.type} · ${step.value}`}
                    </p>
                    <div className="mt-1 h-1.5 bg-muted">
                      <div
                        className="h-full bg-foreground"
                        style={{
                          width: `${
                            previous?.reached && current?.reached
                              ? (current.reached / previous.reached) * 100
                              : 0
                          }%`,
                        }}
                      />
                    </div>
                  </div>
                  <span className="text-[10px]">
                    {formatCount(current?.reached ?? 0)}
                  </span>
                  {index > 0 ? (
                    <span className="text-[10px] text-muted-foreground">
                      −{formatPercent(drop)}
                    </span>
                  ) : null}
                </div>
              );
            })}
          </div>
          {funnelID ? (
            <Button
              className="mt-3"
              onClick={() => {
                void (async () => {
                  try {
                    await deleteAnalyticsFunnel(
                      projectID,
                      tracker.id,
                      funnelID
                    );
                    setFunnels((current) => {
                      const remaining = current.filter(
                        (entry) => entry.id !== funnelID
                      );
                      setFunnelID(remaining[0]?.id ?? "");
                      return remaining;
                    });
                    setActionError("");
                  } catch (error: unknown) {
                    setActionError(actionMessage(error));
                  }
                })();
              }}
              size="sm"
              variant="ghost"
            >
              <Trash2 /> Delete funnel
            </Button>
          ) : null}
        </div>
      ) : null}
      {tab === "explore" ? (
        <>
          <QueryError error={paths.error} />
          <ExploreColumns
            rows={recordRows(paths.data).map((row) => ({
              from: asString(row.from_path),
              to: asString(row.to_path),
              visitors: asNumber(row.visitors),
            }))}
          />
        </>
      ) : null}
    </section>
  );
};

const BreakdownList = ({
  onFilter,
  rows,
}: {
  onFilter: (label: string) => void;
  rows: { label: string; visitors: number }[];
}) => {
  const maximum = Math.max(1, ...rows.map((row) => row.visitors));
  return (
    <div className="mt-4">
      {rows.slice(0, 9).map((row) => (
        <button
          className="flex h-[30px] w-full items-center gap-2 text-left text-[10px]"
          key={row.label}
          onClick={() => onFilter(row.label)}
          type="button"
        >
          <span className="relative min-w-0 flex-1 truncate">
            <span
              className="absolute inset-y-0.5 left-0 bg-muted"
              style={{ width: `${(row.visitors / maximum) * 100}%` }}
            />
            <span className="relative px-1">{row.label}</span>
          </span>
          <span>{formatCount(row.visitors)}</span>
        </button>
      ))}
    </div>
  );
};

const ExploreColumns = ({
  rows,
}: {
  rows: { from: string; to: string; visitors: number }[];
}) => {
  const [selectedStart, setSelectedStart] = useState<string | null>(null);
  const fromPaths = [...new Set(rows.map((row) => row.from))];
  const start = selectedStart ?? fromPaths[0] ?? "/";
  const startPaths = fromPaths.includes(start)
    ? fromPaths
    : [start, ...fromPaths];
  const outgoing = rows.filter((row) => row.from === start);
  return (
    <div className="mt-4 grid gap-4 md:grid-cols-2">
      <div>
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Starting point
        </p>
        <div className="mt-2 grid gap-1">
          {[...new Set(startPaths)].slice(0, 8).map((path) => (
            <button
              className={cn(
                "h-8 border border-border px-2 text-left text-[10px]",
                start === path && "bg-muted"
              )}
              key={path}
              onClick={() => setSelectedStart(path)}
              type="button"
            >
              {path}
            </button>
          ))}
        </div>
      </div>
      <div>
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          1 step after
        </p>
        <div className="mt-2 grid gap-1">
          {outgoing.length === 0 ? (
            <p className="text-[10px] text-muted-foreground">
              No further action
            </p>
          ) : (
            outgoing.map((row) => (
              <button
                className="flex h-8 items-center justify-between border border-border px-2 text-[10px]"
                key={`${row.from}:${row.to}`}
                onClick={() => setSelectedStart(row.to)}
                type="button"
              >
                <span>{row.to}</span>
                <span>{formatCount(row.visitors)}</span>
              </button>
            ))
          )}
        </div>
      </div>
    </div>
  );
};

const VisitorsPage = ({
  filters,
  from,
  projectID,
  to,
  tracker,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  projectID: string;
  to?: number;
  tracker: AnalyticsTracker;
}) => {
  const query = useAnalyticsQuery(projectID, tracker.id, {
    filters,
    from,
    report: "sessions",
    to,
  });
  const rows = recordRows(query.data);
  const [selected, setSelected] = useState<Record<string, unknown>>();
  return (
    <div className="grid min-h-[32rem] lg:grid-cols-[minmax(0,1fr)_20rem]">
      <div className="overflow-x-auto">
        <QueryError error={query.error} />
        <table className="w-full text-[10px]">
          <thead className="border-b border-border text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            <tr>
              <th className="px-3 py-2 text-left">Visitor</th>
              <th className="px-3 py-2 text-left">Views</th>
              <th className="px-3 py-2 text-left">Events</th>
              <th className="px-3 py-2 text-left">Location</th>
              <th className="px-3 py-2 text-left">Browser</th>
              <th className="px-3 py-2 text-left">Last seen</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr
                className="cursor-pointer border-b border-border hover:bg-muted/40"
                key={asString(row.visit_id)}
                onClick={() => setSelected(row)}
              >
                <td className="px-3 py-2 font-mono">
                  {asString(row.distinct_id).slice(0, 12)}
                </td>
                <td className="px-3 py-2">
                  {formatCount(asNumber(row.views))}
                </td>
                <td className="px-3 py-2">
                  {formatCount(asNumber(row.events))}
                </td>
                <td className="px-3 py-2">
                  {analyticsLocationName({
                    city: asString(row.city),
                    country: asString(row.country),
                    region: asString(row.region),
                  })}
                </td>
                <td className="px-3 py-2">
                  {asString(row.browser)} · {asString(row.os)} ·{" "}
                  {asString(row.device)}
                </td>
                <td className="px-3 py-2">
                  {new Date(asNumber(row.last_seen)).toLocaleString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {selected ? (
        <aside className="border-l border-border px-4 py-4">
          <div className="flex items-start justify-between">
            <p className="font-mono text-[10px] break-all">
              {asString(selected.distinct_id)}
            </p>
            <Button
              onClick={() => setSelected(undefined)}
              size="icon"
              variant="ghost"
            >
              <X />
            </Button>
          </div>
          <dl className="mt-4 grid grid-cols-2 gap-2 text-[10px]">
            <dt className="text-muted-foreground">Views</dt>
            <dd>{formatCount(asNumber(selected.views))}</dd>
            <dt className="text-muted-foreground">Events</dt>
            <dd>{formatCount(asNumber(selected.events))}</dd>
            <dt className="text-muted-foreground">Duration</dt>
            <dd>{formatDuration(asNumber(selected.duration))}</dd>
            <dt className="text-muted-foreground">Location</dt>
            <dd>
              {analyticsLocationName({
                city: asString(selected.city),
                country: asString(selected.country),
                region: asString(selected.region),
              })}
            </dd>
          </dl>
        </aside>
      ) : null}
    </div>
  );
};

const HeatmapsPage = ({
  filters,
  from,
  projectID,
  to,
  tracker,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  projectID: string;
  to?: number;
  tracker: AnalyticsTracker;
}) => {
  const [pathname, setPathname] = useState("/");
  const [eventType, setEventType] = useState<"click" | "scroll">("click");
  const [viewport, setViewport] = useState(1440);
  const hosts = tracker.matchingHostnames;
  const [hostname, setHostname] = useState("");
  const heatmapFilters = hostname
    ? [
        ...filters.filter((filter) => filter.dimension !== "hostname"),
        { dimension: "hostname", operator: "is" as const, value: hostname },
      ]
    : filters;
  const pages = useAnalyticsQuery(projectID, tracker.id, {
    dimension: "pathname",
    filters: heatmapFilters,
    from,
    report: "lookup",
    to,
  });
  const query = useAnalyticsQuery(projectID, tracker.id, {
    eventType,
    filters: heatmapFilters,
    from,
    pathname,
    report: "heatmap",
    to,
    viewport,
  });
  const dots = recordRows(query.data).map((row) => ({
    hits: asNumber(row.hits),
    x: asNumber(row.x),
    y: asNumber(row.y),
  }));
  const pageLabels = recordRows(pages.data).map((row) => asString(row.label));
  const pageItems = (
    pageLabels.includes(pathname) ? pageLabels : [pathname, ...pageLabels]
  )
    .filter(Boolean)
    .map((label) => ({ label, value: label }));
  const maximum = Math.max(1, ...dots.map((dot) => dot.hits));
  return (
    <div className="px-5 py-5">
      <QueryError error={pages.error || query.error} />
      <div className="flex flex-wrap items-center gap-2">
        <FieldSelect
          aria-label="Heatmap page"
          className="w-fit min-w-48"
          items={
            pageItems.length === 0
              ? [{ label: pathname, value: pathname }]
              : pageItems
          }
          onValueChange={setPathname}
          value={pathname}
        />
        {hosts.length > 1 ? (
          <FieldSelect
            aria-label="Heatmap hostname"
            className="w-fit min-w-48"
            items={[
              { label: "All hosts", value: "all" },
              ...hosts.map((host) => ({ label: host, value: host })),
            ]}
            onValueChange={(next) => setHostname(next === "all" ? "" : next)}
            value={hostname || "all"}
          />
        ) : null}
        <button
          className={cn(
            "h-8 px-2 text-[9px] uppercase",
            eventType === "click" && "bg-muted"
          )}
          onClick={() => setEventType("click")}
          type="button"
        >
          Clicks
        </button>
        <button
          className={cn(
            "h-8 px-2 text-[9px] uppercase",
            eventType === "scroll" && "bg-muted"
          )}
          onClick={() => setEventType("scroll")}
          type="button"
        >
          Scroll
        </button>
        <FieldSelect
          aria-label="Viewport width"
          className="w-fit min-w-28"
          items={viewportBuckets.map((bucket) => ({
            label: `${bucket}px`,
            value: String(bucket),
          }))}
          onValueChange={(next) => setViewport(Number(next))}
          value={String(viewport)}
        />
      </div>
      <div className="relative mt-4 h-[28rem] border border-border bg-muted/10">
        {dots.map((dot) => (
          <span
            className="absolute size-2 -translate-x-1/2 -translate-y-1/2 bg-foreground"
            key={`${dot.x}:${dot.y}`}
            style={{
              left: `${dot.x}%`,
              opacity: 0.2 + (dot.hits / maximum) * 0.8,
              top: `${dot.y}%`,
            }}
            title={`${dot.hits}`}
          />
        ))}
        {dots.length === 0 ? (
          <p className="grid h-full place-items-center text-[10px] text-muted-foreground">
            No heatmap samples in this range.
          </p>
        ) : null}
      </div>
    </div>
  );
};

const RetentionPage = ({
  filters,
  from,
  projectID,
  to,
  tracker,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  projectID: string;
  to?: number;
  tracker: AnalyticsTracker;
}) => {
  const query = useAnalyticsQuery(projectID, tracker.id, {
    filters,
    from,
    report: "retention",
    to,
  });
  const rows = recordRows(query.data);
  const means = retentionDays.map((day) => {
    const values = rows.map((row) => asNumber(row[`d${day}`]));
    if (values.length === 0) {
      return 0;
    }
    return values.reduce((sum, value) => sum + value, 0) / values.length;
  });
  return (
    <div className="overflow-x-auto px-5 py-5">
      <QueryError error={query.error} />
      <table className="w-full text-[10px]">
        <thead className="border-b border-border text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          <tr>
            <th className="px-2 py-2 text-left">Cohort</th>
            <th className="px-2 py-2 text-left">Size</th>
            {retentionDays.map((day) => (
              <th className="px-2 py-2 text-right" key={day}>
                Day {day}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          <tr className="border-b border-border">
            <td className="px-2 py-2">Mean</td>
            <td className="px-2 py-2">—</td>
            {means.map((value, index) => (
              <td className="px-2 py-2 text-right" key={retentionDays[index]}>
                {formatPercent(value)}
              </td>
            ))}
          </tr>
          {rows.map((row) => (
            <tr className="border-b border-border" key={asString(row.cohort)}>
              <td className="px-2 py-2">
                {new Date(asNumber(row.cohort)).toLocaleDateString()}
              </td>
              <td className="px-2 py-2">{formatCount(asNumber(row.size))}</td>
              {retentionDays.map((day) => (
                <td className="px-2 py-2 text-right" key={day}>
                  {formatPercent(asNumber(row[`d${day}`]))}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
};

const AIPage = ({
  filters,
  from,
  onFilter,
  projectID,
  to,
  tracker,
}: {
  filters: AnalyticsFilter[];
  from?: number;
  onFilter: (filter: AnalyticsFilter) => void;
  projectID: string;
  to?: number;
  tracker: AnalyticsTracker;
}) => {
  const query = useAnalyticsQuery(projectID, tracker.id, {
    filters,
    from,
    report: "bots",
    to,
  });
  const payload =
    query.data && typeof query.data === "object"
      ? (query.data as { hits?: unknown; referrals?: unknown })
      : {};
  const hits = recordRows(payload.hits);
  const referrals = recordRows(payload.referrals);
  return (
    <div className="grid gap-8 px-5 py-5 lg:grid-cols-2">
      <QueryError className="lg:col-span-2" error={query.error} />
      <section className="lg:col-span-2">
        <h2 className="text-sm font-medium">Crawlers</h2>
        <p className="mt-1 max-w-4xl text-[10px] leading-5 text-muted-foreground">
          Server-side HTML requests, not the browser SDK. Current releases count
          OpenAI, Anthropic, and Perplexity agents only when both the user-agent
          and source IP match the provider&apos;s published ranges. Search
          indexing prepares pages for future answers; a question asked by a user
          may cause a live page fetch; training crawlers collect content that
          may be used for model development.
        </p>
        <div className="mt-4 overflow-x-auto">
          <div className="grid min-w-[48rem] grid-cols-[minmax(18rem,2fr)_minmax(10rem,1fr)_minmax(12rem,1.3fr)_4rem] border-b border-border pb-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            <span>What triggered the request</span>
            <span>Verified agent</span>
            <span>Path</span>
            <span className="text-right">Hits</span>
          </div>
          {hits.map((row) => (
            <div
              className="grid min-w-[48rem] grid-cols-[minmax(18rem,2fr)_minmax(10rem,1fr)_minmax(12rem,1.3fr)_4rem] items-start border-b border-border py-2.5 text-[10px]"
              key={`${asString(row.bot_name)}:${asString(row.pathname)}`}
            >
              <span className="pr-5 leading-4">
                {analyticsBotPurpose(
                  asString(row.bot_name),
                  asString(row.bot_kind)
                )}
              </span>
              <span className="font-mono text-muted-foreground">
                {asString(row.bot_name)}
              </span>
              <span className="font-mono break-all">
                {asString(row.pathname)}
              </span>
              <span className="text-right">
                {formatCount(asNumber(row.hits))}
              </span>
            </div>
          ))}
        </div>
      </section>
      <section>
        <h2 className="text-sm font-medium">AI Assistants</h2>
        <p className="mt-1 text-[10px] text-muted-foreground">
          Human pageviews referred from chat engines.
        </p>
        <BreakdownList
          onFilter={(label) =>
            onFilter({ dimension: "source", operator: "is", value: label })
          }
          rows={referrals.map((row) => ({
            label: asString(row.label),
            visitors: asNumber(row.visitors),
          }))}
        />
      </section>
    </div>
  );
};

const FlagsPage = ({
  from,
  projectID,
  to,
  tracker,
}: {
  from?: number;
  projectID: string;
  to?: number;
  tracker: AnalyticsTracker;
}) => {
  const [flags, setFlags] = useState<AnalyticsFlag[]>([]);
  const [experiments, setExperiments] = useState<AnalyticsExperiment[]>([]);
  const [goals, setGoals] = useState<AnalyticsGoal[]>([]);
  const [listError, setListError] = useState("");
  const [actionError, setActionError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [nextFlags, nextExperiments, nextGoals] = await Promise.all([
          fetchAnalyticsFlags(projectID, tracker.id, controller.signal),
          fetchAnalyticsExperiments(projectID, tracker.id, controller.signal),
          fetchAnalyticsGoals(projectID, tracker.id, controller.signal),
        ]);
        setFlags(nextFlags);
        setExperiments(nextExperiments);
        setGoals(nextGoals);
        setListError("");
      } catch (loadError) {
        if (!isAbortError(loadError)) {
          setListError(actionMessage(loadError));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, tracker.id]);
  if (listError) {
    return (
      <div className="px-5 py-5">
        <QueryError error={listError} />
      </div>
    );
  }
  if (flags.length === 0) {
    return (
      <div>
        <AnalyticsSetupGuide tracker={tracker} />
        <div className="px-5 py-6">
          <FlagDialog
            onSaved={(flag) => setFlags([flag])}
            projectID={projectID}
            trackerID={tracker.id}
            trigger={
              <Button>
                <Plus /> Create flag
              </Button>
            }
          />
        </div>
      </div>
    );
  }
  return (
    <div className="px-5 py-5">
      <QueryError error={actionError} />
      <div className="flex gap-2">
        <FlagDialog
          onSaved={(flag) => setFlags((current) => [...current, flag])}
          projectID={projectID}
          trackerID={tracker.id}
          trigger={
            <Button size="sm">
              <Plus /> Flag
            </Button>
          }
        />
        <ExperimentDialog
          flags={flags}
          goals={goals}
          onSaved={(experiment) =>
            setExperiments((current) => [...current, experiment])
          }
          projectID={projectID}
          trackerID={tracker.id}
          trigger={
            <Button size="sm" variant="outline">
              Start experiment
            </Button>
          }
        />
      </div>
      <div className="mt-4">
        {flags.map((flag) => (
          <div
            className="flex items-center justify-between border-b border-border py-3 text-[10px]"
            key={flag.id}
          >
            <div>
              <p className="text-xs">{flag.key}</p>
              <p className="text-muted-foreground">
                {analyticsFlagSummary(flag)}
              </p>
            </div>
            <div className="flex gap-2">
              <FlagDialog
                flag={flag}
                key={flag.updatedAt}
                onSaved={(saved) =>
                  setFlags((current) =>
                    current.map((entry) =>
                      entry.id === saved.id ? saved : entry
                    )
                  )
                }
                projectID={projectID}
                trackerID={tracker.id}
                trigger={
                  <Button size="sm" variant="ghost">
                    Edit
                  </Button>
                }
              />
              <Button
                onClick={() => {
                  void (async () => {
                    try {
                      await deleteAnalyticsFlag(projectID, tracker.id, flag.id);
                      setFlags((current) =>
                        current.filter((entry) => entry.id !== flag.id)
                      );
                      setExperiments((current) =>
                        current.filter((entry) => entry.flagId !== flag.id)
                      );
                      setActionError("");
                    } catch (error: unknown) {
                      setActionError(actionMessage(error));
                    }
                  })();
                }}
                size="sm"
                variant="ghost"
              >
                <Trash2 />
              </Button>
            </div>
          </div>
        ))}
      </div>
      <h3 className="mt-8 text-sm font-medium">Experiments</h3>
      <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
        Ship 100% rolls this variant out to everyone and stops the test. Stop
        leaves the current split.
      </p>
      {experiments.map((experiment) => (
        <ExperimentRow
          experiment={experiment}
          flag={flags.find((flag) => flag.id === experiment.flagId)}
          from={from}
          key={experiment.id}
          onChanged={(saved) => {
            setExperiments((current) =>
              current.map((entry) => (entry.id === saved.id ? saved : entry))
            );
            void (async () => {
              try {
                setFlags(await fetchAnalyticsFlags(projectID, tracker.id));
                setActionError("");
              } catch (error: unknown) {
                setActionError(actionMessage(error));
              }
            })();
          }}
          projectID={projectID}
          to={to}
          trackerID={tracker.id}
        />
      ))}
    </div>
  );
};

const ExperimentRow = ({
  experiment,
  flag,
  from,
  onChanged,
  projectID,
  to,
  trackerID,
}: {
  experiment: AnalyticsExperiment;
  flag?: AnalyticsFlag;
  from?: number;
  onChanged: (experiment: AnalyticsExperiment) => void;
  projectID: string;
  to?: number;
  trackerID: string;
}) => {
  const [actionError, setActionError] = useState("");
  const query = useAnalyticsQuery(projectID, trackerID, {
    experimentId: experiment.id,
    from,
    report: "experiment",
    to,
  });
  const rows = recordRows(query.data);
  const control = rows.find(
    (row) => asString(row.variant) === experiment.controlVariant
  );
  const controlRate =
    control && asNumber(control.exposed) > 0
      ? asNumber(control.converted) / asNumber(control.exposed)
      : 0;
  return (
    <div className="mt-3 border border-border p-3">
      <QueryError error={query.error || actionError} />
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs">
          {flag?.key ?? experiment.flagId}{" "}
          <span className="text-muted-foreground">
            {experiment.endedAt ? "stopped" : "running"}
          </span>
        </p>
        {experiment.endedAt ? null : (
          <Button
            onClick={() => {
              void (async () => {
                try {
                  const saved = await stopAnalyticsExperiment(
                    projectID,
                    trackerID,
                    experiment.id,
                    experiment.updatedAt
                  );
                  setActionError("");
                  onChanged(saved);
                } catch (error: unknown) {
                  setActionError(actionMessage(error));
                }
              })();
            }}
            size="sm"
            variant="outline"
          >
            Stop
          </Button>
        )}
      </div>
      <div className="mt-3 grid gap-2">
        {rows.map((row) => {
          const exposed = asNumber(row.exposed);
          const converted = asNumber(row.converted);
          const interval = wilsonInterval(converted, exposed);
          const lift = controlRate === 0 ? 0 : interval.rate / controlRate - 1;
          const variant = asString(row.variant);
          return (
            <div className="grid grid-cols-6 gap-2 text-[10px]" key={variant}>
              <span>{variant}</span>
              <span>{formatCount(exposed)} exposed</span>
              <span>{formatCount(converted)} conv</span>
              <span>
                {formatPercent(interval.rate)} ({formatPercent(interval.low)}–
                {formatPercent(interval.high)})
              </span>
              <span>
                {variant === experiment.controlVariant
                  ? "control"
                  : `${lift >= 0 ? "+" : ""}${Math.round(lift * 100)}% lift`}
              </span>
              {experiment.endedAt ? null : (
                <Button
                  onClick={() => {
                    void (async () => {
                      try {
                        const saved = await shipAnalyticsExperiment(
                          projectID,
                          trackerID,
                          experiment.id,
                          {
                            expectedUpdatedAt: experiment.updatedAt,
                            variant,
                          }
                        );
                        setActionError("");
                        onChanged(saved);
                      } catch (error: unknown) {
                        setActionError(actionMessage(error));
                      }
                    })();
                  }}
                  size="sm"
                  title="Stop the experiment and send all traffic to this variant"
                  variant="ghost"
                >
                  Ship 100%
                </Button>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
};

const ChartsPage = ({
  from,
  projectID,
  to,
  tracker,
}: {
  from?: number;
  projectID: string;
  to?: number;
  tracker: AnalyticsTracker;
}) => {
  const [charts, setCharts] = useState<AnalyticsChart[]>([]);
  const [listError, setListError] = useState("");
  const [actionError, setActionError] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setCharts(
          await fetchAnalyticsCharts(projectID, tracker.id, controller.signal)
        );
        setListError("");
      } catch (loadError) {
        if (!isAbortError(loadError)) {
          setListError(actionMessage(loadError));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, tracker.id]);
  return (
    <div className="px-5 py-5">
      <QueryError error={listError || actionError} />
      <AnalyticsChartDialog
        onSaved={(chart) => setCharts((current) => [...current, chart])}
        projectID={projectID}
        trackerID={tracker.id}
        trigger={
          <Button>
            <Plus /> Chart
          </Button>
        }
      />
      <div className="mt-4 grid gap-6">
        {charts.map((chart) => (
          <ChartBlock
            chart={chart}
            from={from}
            key={chart.id}
            onDeleted={() =>
              setCharts((current) =>
                current.filter((entry) => entry.id !== chart.id)
              )
            }
            onError={setActionError}
            projectID={projectID}
            to={to}
            trackerID={tracker.id}
          />
        ))}
      </div>
    </div>
  );
};

const ChartBlock = ({
  chart,
  from,
  onDeleted,
  onError,
  projectID,
  to,
  trackerID,
}: {
  chart: AnalyticsChart;
  from?: number;
  onDeleted: () => void;
  onError: (message: string) => void;
  projectID: string;
  to?: number;
  trackerID: string;
}) => {
  const query = useAnalyticsQuery(projectID, trackerID, {
    from,
    report: "sql",
    sql: chart.sql,
    to,
  });
  const rows = recordRows(query.data);
  const points = rows.map((row) => ({
    observedAt: asNumber(row.time),
    value: asNumber(row.value),
  }));
  return (
    <section className="border-b border-border pb-5">
      <QueryError error={query.error} />
      <div className="flex items-center justify-between">
        <h3 className="text-sm">{chart.title}</h3>
        <Button
          onClick={() => {
            void (async () => {
              try {
                await deleteAnalyticsChart(projectID, trackerID, chart.id);
                onDeleted();
              } catch (error: unknown) {
                onError(actionMessage(error));
              }
            })();
          }}
          size="sm"
          variant="ghost"
        >
          <Trash2 />
        </Button>
      </div>
      {chart.visualization === "table" ? (
        <div className="mt-3 overflow-x-auto">
          <table className="w-full text-[10px]">
            <thead>
              <tr className="border-b border-border text-muted-foreground">
                {Object.keys(rows[0] ?? { time: 0, value: 0 }).map((column) => (
                  <th className="px-2 py-1 text-left" key={column}>
                    {column}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row, index) => (
                <tr
                  className="border-b border-border"
                  key={`${chart.id}:${index}`}
                >
                  {Object.values(row).map((value, column) => (
                    <td
                      className="px-2 py-1"
                      key={`${chart.id}:${index}:${column}`}
                    >
                      {String(value)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
      {chart.visualization === "value" ? (
        <p className="mt-4 text-2xl">
          {formatCount(points.at(-1)?.value ?? 0)}
        </p>
      ) : null}
      {chart.visualization === "table" ||
      chart.visualization === "value" ? null : (
        <MetricChart
          emptyLabel="No rows"
          formatValue={formatCount}
          from={from ?? points[0]?.observedAt ?? 0}
          minimumMaximum={1}
          points={points}
          series={[
            {
              color: metricPalette[0],
              label: chart.legend || chart.title,
              value: (point) => point.value,
            },
          ]}
          title=""
          to={to ?? points.at(-1)?.observedAt ?? 0}
          visualization={
            chart.visualization === "bar" || chart.visualization === "line"
              ? chart.visualization
              : "area"
          }
        />
      )}
    </section>
  );
};

const SettingsPage = ({
  hostnames,
  onDeleted,
  onSaved,
  projectID,
  tracker,
  trackers,
}: {
  hostnames: string[];
  onDeleted: () => void;
  onSaved: (tracker: AnalyticsTracker) => void;
  projectID: string;
  tracker: AnalyticsTracker;
  trackers: AnalyticsTracker[];
}) => {
  const [actionError, setActionError] = useState("");
  return (
    <div>
      <QueryError error={actionError} />
      <section className="border-b border-border px-5 py-6">
        <div className="flex items-start justify-between gap-3">
          <div>
            <h2 className="text-sm font-medium">{tracker.name}</h2>
            <p className="mt-1 text-[10px] text-muted-foreground">
              Root {tracker.rootDomain} · {tracker.internalOfrepUrl}
            </p>
            <p className="mt-2 text-[10px] text-muted-foreground">
              Matching hostnames:{" "}
              {tracker.matchingHostnames.join(", ") || "none yet"}
            </p>
          </div>
          <div className="flex gap-2">
            <TrackerDialog
              hostnames={hostnames}
              onSaved={onSaved}
              projectID={projectID}
              tracker={tracker}
              trackers={trackers}
              trigger={
                <Button size="sm" variant="outline">
                  Edit
                </Button>
              }
            />
            <Button
              onClick={() => {
                void (async () => {
                  try {
                    await deleteAnalyticsTracker(projectID, tracker.id);
                    setActionError("");
                    onDeleted();
                  } catch (error: unknown) {
                    setActionError(actionMessage(error));
                  }
                })();
              }}
              size="sm"
              variant="ghost"
            >
              Delete
            </Button>
          </div>
        </div>
      </section>
      <AnalyticsSetupGuide tracker={tracker} />
    </div>
  );
};

export const ServiceAnalyticsSnippet = ({
  projectID,
  trackedBy,
}: {
  projectID: string;
  trackedBy?: {
    id: string;
    name: string;
    rootDomain: string;
  }[];
}) => {
  if (!trackedBy || trackedBy.length === 0) {
    return (
      <section className="border-b border-border px-5 py-6 lg:px-7">
        <h2 className="text-sm font-medium">Web analytics</h2>
        <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
          None of this service&apos;s domains match a project tracker.
        </p>
        <Button
          className="mt-3"
          render={
            <Link to={`/projects/${encodeURIComponent(projectID)}/analytics`} />
          }
          size="sm"
        >
          Create tracker on the project
        </Button>
      </section>
    );
  }
  const configurations = trackedBy
    .toSorted((left, right) => right.rootDomain.length - left.rootDomain.length)
    .map((tracker) => ({
      cookieDomain: analyticsCookieDomain(tracker.rootDomain) || undefined,
      rootDomain: tracker.rootDomain,
    }));
  const snippet = `// analytics.ts — rebuild after changing the identity mode or root domains.
import { createAnalytics } from "@platformd/analytics";

const mode = "opt-out" as const; // Or "opt-in" / "cookieless".
const trackerConfigurations = ${JSON.stringify(configurations, null, 2)} as const;
const configuration = trackerConfigurations.find(
  ({ rootDomain }) =>
    location.hostname === rootDomain || location.hostname.endsWith(\`.\${rootDomain}\`)
);

if (!configuration) {
  throw new Error("No platformd analytics tracker matches this hostname");
}

export const analytics = createAnalytics({ ...configuration, mode });`;
  return (
    <section className="border-b border-border px-5 py-6 lg:px-7">
      <h2 className="text-sm font-medium">Web analytics</h2>
      <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
        Tracked by {trackedBy.map((tracker) => tracker.rootDomain).join(", ")}.
        Install the package once; OpenFeature examples live on the tracker.
      </p>
      <p className="mt-3 font-mono text-[10px] text-muted-foreground">
        npm install @platformd/analytics
      </p>
      <HighlightedSnippet
        className="mt-3"
        language="typescript"
        value={snippet}
      />
      <div className="mt-3 flex flex-wrap gap-2">
        {trackedBy.map((tracker) => (
          <Button
            key={tracker.id}
            render={
              <Link
                to={`/projects/${encodeURIComponent(projectID)}/analytics?tracker=${encodeURIComponent(tracker.id)}&analyticsPage=settings`}
              />
            }
            size="sm"
            variant="outline"
          >
            {tracker.rootDomain} settings
          </Button>
        ))}
      </div>
    </section>
  );
};
