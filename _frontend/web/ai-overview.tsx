import { LoaderCircle, RefreshCw } from "lucide-react";
import { useQueryStates } from "nuqs";
import { useEffect, useMemo, useState } from "react";

import {
  emptyPricedUsage,
  formatPricedUsageCost,
  formatUserCost,
  overviewAnalysis,
  overviewStep,
  userRows,
} from "@/ai-overview-model";
import type {
  AiTimelinePoint,
  PricedUsage,
  UserRow,
} from "@/ai-overview-model";
import { formatAiCost } from "@/ai-price";
import { fetchAiOverview } from "@/api";
import type { AiOverview, MetricScope } from "@/api";
import { Button } from "@/components/ui/button";
import { MetricChart } from "@/metric-chart";
import { metricPalette } from "@/service-metric-model";
import { aiTimeRangeQueryParsers } from "@/telemetry-query-state";
import {
  TelemetryTimeRangePicker,
  telemetryTimeBounds,
} from "@/telemetry-time-range";

const RETENTION_MILLISECONDS = 30 * 24 * 60 * 60_000;

const compactNumber = new Intl.NumberFormat("en-US", {
  maximumFractionDigits: 1,
  notation: "compact",
});

const formatCount = (value: number) => compactNumber.format(value);

const formatTokens = (value: number) => `${compactNumber.format(value)} tok`;

const formatLatency = (seconds: number) => {
  if (seconds < 0.001) {
    return `${(seconds * 1_000_000).toFixed(0)} µs`;
  }
  if (seconds < 1) {
    return `${(seconds * 1000).toFixed(seconds < 0.1 ? 1 : 0)} ms`;
  }
  if (seconds < 60) {
    return `${seconds.toFixed(seconds < 10 ? 2 : 1)} s`;
  }
  return `${(seconds / 60).toFixed(1)} min`;
};

const EmptySection = ({ children }: { children: string }) => (
  <p className="px-4 py-10 text-center text-[9px] text-muted-foreground">
    {children}
  </p>
);

const SectionHeading = ({
  detail,
  title,
}: {
  detail?: string;
  title: string;
}) => (
  <header className="flex min-h-12 items-center justify-between gap-4 border-b border-border px-4 py-2.5">
    <h2 className="text-xs font-medium tracking-[0.12em] uppercase">{title}</h2>
    {detail ? (
      <span className="text-[9px] text-muted-foreground">{detail}</span>
    ) : null}
  </header>
);

const AiKpis = ({
  cost,
  overview,
  tokens,
}: {
  cost: PricedUsage;
  overview?: AiOverview;
  tokens: number;
}) => {
  const summary = overview?.summary;
  const modelCost = formatPricedUsageCost(
    cost,
    (summary?.generationCount ?? 0) > 0
  );
  const kpis = [
    ["Agent runs", formatCount(summary?.agentRunCount ?? 0)],
    ["Generations", formatCount(summary?.generationCount ?? 0)],
    ["Tool calls", formatCount(summary?.toolCallCount ?? 0)],
    ["Tokens", formatTokens(tokens)],
    ["Model cost", modelCost],
    ["Errors", formatCount(summary?.errorCount ?? 0)],
    ["Users", formatCount(summary?.userCount ?? 0)],
    ["Sessions", formatCount(summary?.sessionCount ?? 0)],
  ];
  return (
    <section className="grid grid-cols-2 border-b border-border sm:grid-cols-4 xl:grid-cols-8">
      {kpis.map(([label, value]) => (
        <div
          className="min-w-0 border-r border-border px-4 py-3 last:border-r-0"
          key={label}
        >
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            {label}
          </p>
          <p
            className={
              label === "Errors" && (summary?.errorCount ?? 0) > 0
                ? "mt-1 truncate text-sm text-destructive tabular-nums"
                : "mt-1 truncate text-sm tabular-nums"
            }
            title={value}
          >
            {value}
          </p>
          {label === "Users" && (summary?.agentRunCount ?? 0) > 0 ? (
            <p className="mt-0.5 text-[8px] text-muted-foreground">
              {summary?.identifiedAgentRunCount.toLocaleString()} /{" "}
              {summary?.agentRunCount.toLocaleString()} runs identified
            </p>
          ) : null}
        </div>
      ))}
    </section>
  );
};

const AiCharts = ({
  from,
  points,
  to,
}: {
  from: number;
  points: AiTimelinePoint[];
  to: number;
}) => (
  <section className="grid border-b border-border 2xl:grid-cols-3">
    <div className="min-w-0 2xl:border-r 2xl:border-border">
      <MetricChart<AiTimelinePoint>
        emptyLabel="No AI activity in this range"
        formatValue={formatCount}
        from={from}
        minimumMaximum={5}
        points={points}
        series={[
          {
            color: metricPalette[0],
            label: "Agent runs",
            value: (point) => point.agentRuns,
          },
          {
            color: metricPalette[1],
            label: "Generations",
            value: (point) => point.generations,
          },
          {
            color: metricPalette[2],
            label: "Tool calls",
            value: (point) => point.toolCalls,
          },
          {
            color: "hsl(var(--destructive))",
            label: "Errors",
            value: (point) => point.errors,
          },
        ]}
        title="Activity"
        to={to}
        visualization="line"
      />
    </div>
    <div className="min-w-0 border-t border-border 2xl:border-t-0 2xl:border-r">
      <MetricChart<AiTimelinePoint>
        emptyLabel="No token usage in this range"
        formatValue={formatTokens}
        from={from}
        minimumMaximum={100}
        points={points}
        series={[
          {
            color: metricPalette[3],
            label: "Input",
            value: (point) => point.inputTokens,
          },
          {
            color: metricPalette[4],
            label: "Output",
            value: (point) => point.outputTokens,
          },
        ]}
        title="Model usage"
        to={to}
      />
    </div>
    <div className="min-w-0 border-t border-border 2xl:border-t-0">
      <MetricChart<AiTimelinePoint>
        emptyLabel="Model cost unavailable in this range"
        formatValue={(value) => formatAiCost(value / 100)}
        from={from}
        minimumMaximum={1}
        points={points}
        series={[
          {
            color: metricPalette[1],
            formatValue: (value, point) =>
              `${formatAiCost(value / 100, point.estimatedCost)}${point.partialCost ? "+" : ""}`,
            label: "Cost",
            value: (point) => (point.hasPrice ? point.cost : undefined),
          },
        ]}
        title="Model cost"
        to={to}
      />
    </div>
  </section>
);

const AiModels = ({
  modelCount,
  models,
  usageByModel,
}: {
  modelCount: number;
  models: AiOverview["models"];
  usageByModel: Map<string, PricedUsage>;
}) => {
  const detail =
    modelCount > models.length
      ? `Top ${models.length.toLocaleString()} of ${modelCount.toLocaleString()} models by calls`
      : "Token totals exclude duplicate agent summaries";
  return (
    <section className="border-b border-border">
      <SectionHeading detail={detail} title="Models" />
      {models.length === 0 ? (
        <EmptySection>No model generations in this range.</EmptySection>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[920px] text-left text-[9px]">
            <thead className="text-[8px] tracking-[0.08em] text-muted-foreground uppercase">
              <tr className="border-b border-border/70">
                <th className="px-4 py-2 font-normal">Model</th>
                <th className="px-3 py-2 text-right font-normal">Calls</th>
                <th className="px-3 py-2 text-right font-normal">Input</th>
                <th className="px-3 py-2 text-right font-normal">Output</th>
                <th className="px-3 py-2 text-right font-normal">Cache read</th>
                <th className="px-3 py-2 text-right font-normal">
                  Cache write
                </th>
                <th className="px-3 py-2 text-right font-normal">Reasoning</th>
                <th className="px-3 py-2 text-right font-normal">Cost</th>
                <th className="px-3 py-2 text-right font-normal">p50</th>
                <th className="px-4 py-2 text-right font-normal">p95 / p99</th>
              </tr>
            </thead>
            <tbody>
              {models.map((model) => {
                const usage =
                  usageByModel.get(`${model.provider}\u0000${model.model}`) ??
                  emptyPricedUsage();
                return (
                  <tr
                    className="border-b border-border/50 last:border-b-0"
                    key={`${model.provider}:${model.model}`}
                  >
                    <td className="px-4 py-2.5">
                      <span className="font-medium">
                        {model.model || "Unknown model"}
                      </span>
                      <span className="ml-2 text-muted-foreground">
                        {model.provider || "unknown provider"}
                      </span>
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {model.generationCount.toLocaleString()}
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {formatTokens(usage.inputTokens)}
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {formatTokens(usage.outputTokens)}
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {formatTokens(usage.cacheReadTokens)}
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {formatTokens(usage.cacheWriteTokens)}
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {formatTokens(usage.reasoningTokens)}
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {formatPricedUsageCost(usage)}
                    </td>
                    <td className="px-3 py-2.5 text-right tabular-nums">
                      {formatLatency(model.p50LatencySeconds)}
                    </td>
                    <td className="px-4 py-2.5 text-right tabular-nums">
                      {formatLatency(model.p95LatencySeconds)} /{" "}
                      {formatLatency(model.p99LatencySeconds)}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
};

const AiAgentsAndUsers = ({
  overview,
  users,
}: {
  overview?: AiOverview;
  users: UserRow[];
}) => {
  const agents = overview?.agents ?? [];
  const summary = overview?.summary;
  const coverage = summary
    ? `${summary.identifiedAgentRunCount.toLocaleString()} of ${summary.agentRunCount.toLocaleString()} runs identified`
    : undefined;
  let agentRanking: string | undefined;
  let userRanking: string | undefined;
  if (summary) {
    agentRanking =
      summary.agentCount > agents.length
        ? `Top ${agents.length.toLocaleString()} of ${summary.agentCount.toLocaleString()} agents by runs`
        : "Ranked by runs";
    userRanking =
      summary.userCount > users.length
        ? `Top ${users.length.toLocaleString()} of ${summary.userCount.toLocaleString()} users by token volume`
        : "Ranked by token volume";
  }
  return (
    <div className="grid border-b border-border xl:grid-cols-2">
      <section className="min-w-0 xl:border-r xl:border-border">
        <SectionHeading detail={agentRanking} title="Agents" />
        {agents.length === 0 ? (
          <EmptySection>No agent runs in this range.</EmptySection>
        ) : (
          <div className="divide-y divide-border/60">
            {agents.map((agent) => (
              <div
                className="grid grid-cols-[minmax(0,1fr)_5rem_5rem_8rem] items-center gap-3 px-4 py-2.5 text-[9px]"
                key={agent.agent}
              >
                <div className="min-w-0">
                  <p className="truncate font-medium">
                    {agent.agent || "Unnamed agent"}
                  </p>
                  <p className="mt-0.5 text-[8px] text-muted-foreground">
                    {agent.userCount.toLocaleString()} users ·{" "}
                    {agent.errorCount.toLocaleString()} errors
                  </p>
                </div>
                <span className="text-right tabular-nums">
                  {agent.runCount.toLocaleString()} runs
                </span>
                <span className="text-right text-muted-foreground tabular-nums">
                  p50 {formatLatency(agent.p50LatencySeconds)}
                </span>
                <span className="text-right text-muted-foreground tabular-nums">
                  p95 {formatLatency(agent.p95LatencySeconds)} · p99{" "}
                  {formatLatency(agent.p99LatencySeconds)}
                </span>
              </div>
            ))}
          </div>
        )}
      </section>
      <section className="min-w-0 border-t border-border xl:border-t-0">
        <SectionHeading
          detail={
            coverage && userRanking
              ? `${coverage} · ${userRanking}`
              : (coverage ?? userRanking)
          }
          title="User consumption"
        />
        {users.length === 0 ? (
          <EmptySection>No user metadata in this range.</EmptySection>
        ) : (
          <div className="divide-y divide-border/60">
            {users.map((user) => (
              <div
                className="grid grid-cols-[minmax(0,1fr)_4.5rem_5rem_6rem] items-center gap-3 px-4 py-2.5 text-[9px]"
                key={user.userId}
              >
                <div className="min-w-0">
                  <code className="block truncate" title={user.userId}>
                    {user.userId}
                  </code>
                  <p className="mt-0.5 text-[8px] text-muted-foreground">
                    {user.sessions.toLocaleString()} sessions ·{" "}
                    {user.generations.toLocaleString()} generations
                  </p>
                </div>
                <span className="text-right text-muted-foreground tabular-nums">
                  {user.runs.toLocaleString()} runs
                </span>
                <span className="text-right text-muted-foreground tabular-nums">
                  {formatTokens(user.tokens)}
                </span>
                <span className="text-right tabular-nums">
                  {formatUserCost(user)}
                </span>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
};

const AiLatency = ({ rows }: { rows: AiOverview["latency"] }) => (
  <section>
    <SectionHeading
      detail="Wall-clock span duration"
      title="Latency percentiles"
    />
    {rows.length === 0 ? (
      <EmptySection>No model or tool latency in this range.</EmptySection>
    ) : (
      <div className="overflow-x-auto">
        <table className="w-full min-w-[620px] text-left text-[9px]">
          <thead className="text-[8px] tracking-[0.08em] text-muted-foreground uppercase">
            <tr className="border-b border-border/70">
              <th className="px-4 py-2 font-normal">Observation</th>
              <th className="px-3 py-2 text-right font-normal">Count</th>
              <th className="px-3 py-2 text-right font-normal">p50</th>
              <th className="px-3 py-2 text-right font-normal">p90</th>
              <th className="px-3 py-2 text-right font-normal">p95</th>
              <th className="px-4 py-2 text-right font-normal">p99</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr
                className="border-b border-border/50 last:border-b-0"
                key={`${row.kind}:${row.name}`}
              >
                <td className="px-4 py-2.5">
                  <span className="mr-2 text-[8px] tracking-[0.08em] text-muted-foreground uppercase">
                    {row.kind}
                  </span>
                  {row.name}
                </td>
                <td className="px-3 py-2.5 text-right tabular-nums">
                  {row.count.toLocaleString()}
                </td>
                <td className="px-3 py-2.5 text-right tabular-nums">
                  {formatLatency(row.p50LatencySeconds)}
                </td>
                <td className="px-3 py-2.5 text-right tabular-nums">
                  {formatLatency(row.p90LatencySeconds)}
                </td>
                <td className="px-3 py-2.5 text-right tabular-nums">
                  {formatLatency(row.p95LatencySeconds)}
                </td>
                <td className="px-4 py-2.5 text-right tabular-nums">
                  {formatLatency(row.p99LatencySeconds)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )}
  </section>
);

export const AiOverviewView = ({ scope }: { scope: MetricScope }) => {
  const [{ aiTimeFrom, aiTimeRange, aiTimeTo }, setTimeState] = useQueryStates(
    aiTimeRangeQueryParsers
  );
  const [now, setNow] = useState(() => Date.now());
  const [revision, setRevision] = useState(0);
  const [overview, setOverview] = useState<AiOverview>();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const projectID = scope.kind === "installation" ? "" : scope.projectID;
  const serviceID = scope.kind === "service" ? scope.serviceID : "";
  const stableScope = useMemo<MetricScope>(() => {
    if (scope.kind === "installation") {
      return { kind: "installation" };
    }
    if (scope.kind === "project") {
      return { kind: "project", projectID };
    }
    return { kind: "service", projectID, serviceID };
  }, [projectID, scope.kind, serviceID]);
  const selectedRange = useMemo(
    () => ({ from: aiTimeFrom, range: aiTimeRange, to: aiTimeTo }),
    [aiTimeFrom, aiTimeRange, aiTimeTo]
  );
  const bounds = useMemo(() => {
    const selected = telemetryTimeBounds(selectedRange, now);
    return {
      from: selected.from ?? now - RETENTION_MILLISECONDS,
      to: selected.to ?? now,
    };
  }, [now, selectedRange]);
  const refresh = () => {
    setNow(Date.now());
    setRevision((current) => current + 1);
  };

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      setLoading(true);
      setError("");
      setOverview(undefined);
      try {
        setOverview(
          await fetchAiOverview(
            stableScope,
            {
              from: bounds.from,
              step: overviewStep(bounds.from, bounds.to),
              to: bounds.to,
            },
            controller.signal
          )
        );
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load AI overview"
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
  }, [bounds.from, bounds.to, revision, stableScope]);

  const users = useMemo(() => (overview ? userRows(overview) : []), [overview]);
  const analysis = useMemo(
    () =>
      overview
        ? overviewAnalysis(overview)
        : {
            byModel: new Map<string, PricedUsage>(),
            points: [] as AiTimelinePoint[],
            total: emptyPricedUsage(),
          },
    [overview]
  );
  const { total } = analysis;
  const tokens = total.inputTokens + total.outputTokens;

  return (
    <div aria-busy={loading} className="min-h-full">
      <header className="flex min-h-16 flex-wrap items-center gap-3 border-b border-border px-5 py-3">
        <div className="mr-auto">
          <h1 className="text-sm font-medium">AI Overview</h1>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Agent activity, model usage, cost, users, and latency from traced
            spans.
          </p>
        </div>
        {loading ? (
          <span className="flex items-center gap-1.5 text-[9px] text-muted-foreground">
            <LoaderCircle className="size-3 animate-spin" /> Updating
          </span>
        ) : null}
        <TelemetryTimeRangePicker
          onChange={(value) => {
            setNow(Date.now());
            void setTimeState({
              aiTimeFrom: value.from,
              aiTimeRange: value.range,
              aiTimeTo: value.to,
            });
          }}
          value={selectedRange}
        />
        <Button
          aria-label="Refresh AI overview"
          onClick={refresh}
          size="icon"
          variant="outline"
        >
          <RefreshCw className={loading ? "animate-spin" : ""} />
        </Button>
      </header>

      {error ? (
        <p className="border-b border-destructive/35 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}

      <AiKpis cost={total} overview={overview} tokens={tokens} />
      <AiCharts from={bounds.from} points={analysis.points} to={bounds.to} />
      <AiModels
        modelCount={overview?.summary.modelCount ?? 0}
        models={overview?.models ?? []}
        usageByModel={analysis.byModel}
      />
      <AiAgentsAndUsers overview={overview} users={users} />
      <AiLatency rows={overview?.latency ?? []} />
    </div>
  );
};
