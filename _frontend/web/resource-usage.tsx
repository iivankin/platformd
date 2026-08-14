import { Activity, LoaderCircle, Server } from "lucide-react";
import { useMemo, useState } from "react";

import type {
  ResourceUsage as Usage,
  ResourceUsageHistory,
  ResourceUsageKind,
  ResourceUsageRange,
} from "@/api";
import { CustomMetrics } from "@/custom-metrics";
import { MetricChart } from "@/metric-chart";
import type { MetricSeries } from "@/metric-chart";
import {
  useCurrentResourceUsage,
  useResourceUsageHistory,
} from "@/use-resource-usage";
import { useScopeUsage } from "@/use-scope-usage";

const emptyPoints: ResourceUsageHistory["points"] = [];

const ranges: { label: string; value: ResourceUsageRange }[] = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "1d", value: "1d" },
  { label: "7d", value: "7d" },
  { label: "30d", value: "30d" },
];

const chartColors = {
  danger: "#fb7185",
  primary: "#38bdf8",
  secondary: "#fbbf24",
} as const;

type UsagePoint = ResourceUsageHistory["points"][number];

const cpuSeries: MetricSeries<UsagePoint>[] = [
  {
    color: chartColors.primary,
    label: "Average",
    value: (point) => point.cpuMillicores,
  },
  {
    color: chartColors.primary,
    label: "Peak (2s avg)",
    strokeDasharray: "3 2",
    value: (point) => point.cpuPeakMillicores,
  },
];

const memorySeries: MetricSeries<UsagePoint>[] = [
  {
    color: chartColors.secondary,
    label: "Average",
    value: (point) => point.memoryBytes,
  },
  {
    color: chartColors.secondary,
    label: "Peak",
    strokeDasharray: "3 2",
    value: (point) => point.memoryPeakBytes,
  },
];

const networkSeries: MetricSeries<UsagePoint>[] = [
  {
    color: chartColors.primary,
    label: "Ingress",
    value: (point) => point.networkIngressBytesPerSecond,
  },
  {
    color: chartColors.primary,
    label: "Ingress peak (2s avg)",
    strokeDasharray: "3 2",
    value: (point) => point.networkIngressPeakBytesPerSecond,
  },
  {
    color: chartColors.secondary,
    label: "Egress",
    value: (point) => point.networkEgressBytesPerSecond,
  },
  {
    color: chartColors.secondary,
    label: "Egress peak (2s avg)",
    strokeDasharray: "3 2",
    value: (point) => point.networkEgressPeakBytesPerSecond,
  },
];

const diskSeries: MetricSeries<UsagePoint>[] = [
  {
    color: "#34d399",
    label: "Volumes",
    value: (point) => point.diskBytes,
  },
];

const breakdownColors = [
  "#38bdf8",
  "#34d399",
  "#fbbf24",
  "#fb7185",
  "#a78bfa",
  "#22d3ee",
  "#f97316",
  "#84cc16",
] as const;

const breakdownSeriesFor = (
  history: ResourceUsageHistory | null,
  value: (point: UsagePoint) => number | undefined
): MetricSeries<UsagePoint>[] =>
  (history?.series ?? [])
    .filter((item) => item.points.some((point) => value(point) !== undefined))
    .map((item, index) => ({
      color:
        breakdownColors[index % breakdownColors.length] ?? chartColors.primary,
      label: item.name,
      points: item.points,
      value,
    }));

const cpuBreakdownValue = (point: UsagePoint) => point.cpuMillicores;
const memoryBreakdownValue = (point: UsagePoint) => point.memoryBytes;
const diskBreakdownValue = (point: UsagePoint) => point.diskBytes;
const networkBreakdownValue = (point: UsagePoint) => {
  const ingress = point.networkIngressBytesPerSecond;
  const egress = point.networkEgressBytesPerSecond;
  return ingress === undefined || egress === undefined
    ? undefined
    : ingress + egress;
};
const httpRequestBreakdownValue = (point: UsagePoint) =>
  point.proxy?.http.requestsPerSecond;
const httpLatencyBreakdownValue = (point: UsagePoint) =>
  point.proxy?.http.latencyP95Millis;
const tcpBreakdownValue = (point: UsagePoint) =>
  point.proxy?.tcp.connectionsPerSecond;
const udpBreakdownValue = (point: UsagePoint) => {
  const ingress = point.proxy?.udp.ingressPacketsPerSecond;
  const egress = point.proxy?.udp.egressPacketsPerSecond;
  return ingress === undefined || egress === undefined
    ? undefined
    : ingress + egress;
};

const httpRequestSeries: MetricSeries<UsagePoint>[] = [
  {
    color: chartColors.primary,
    label: "Requests avg",
    value: (point) => point.proxy?.http.requestsPerSecond,
  },
  {
    color: chartColors.primary,
    label: "Requests peak (1s)",
    strokeDasharray: "3 2",
    value: (point) => point.proxy?.http.requestsPeakPerSecond,
  },
  {
    color: chartColors.secondary,
    label: "4xx",
    value: (point) => point.proxy?.http.responses4xxPerSecond,
  },
  {
    color: chartColors.danger,
    label: "5xx",
    value: (point) => point.proxy?.http.responses5xxPerSecond,
  },
];

const httpLatencySeries: MetricSeries<UsagePoint>[] = [
  {
    color: chartColors.primary,
    label: "p50",
    value: (point) => point.proxy?.http.latencyP50Millis,
  },
  {
    color: chartColors.secondary,
    label: "p95",
    value: (point) => point.proxy?.http.latencyP95Millis,
  },
  {
    color: chartColors.danger,
    label: "p99",
    value: (point) => point.proxy?.http.latencyP99Millis,
  },
];

const tcpSeries: MetricSeries<UsagePoint>[] = [
  {
    color: chartColors.primary,
    label: "Connections avg",
    value: (point) => point.proxy?.tcp.connectionsPerSecond,
  },
  {
    color: chartColors.primary,
    label: "Connections peak (1s)",
    strokeDasharray: "3 2",
    value: (point) => point.proxy?.tcp.connectionsPeakPerSecond,
  },
];

const udpSeries: MetricSeries<UsagePoint>[] = [
  {
    color: chartColors.primary,
    label: "Ingress avg",
    value: (point) => point.proxy?.udp.ingressPacketsPerSecond,
  },
  {
    color: chartColors.primary,
    label: "Ingress peak (1s)",
    strokeDasharray: "3 2",
    value: (point) => point.proxy?.udp.ingressPacketsPeakPerSecond,
  },
  {
    color: chartColors.secondary,
    label: "Egress avg",
    value: (point) => point.proxy?.udp.egressPacketsPerSecond,
  },
  {
    color: chartColors.secondary,
    label: "Egress peak (1s)",
    strokeDasharray: "3 2",
    value: (point) => point.proxy?.udp.egressPacketsPeakPerSecond,
  },
];

const formatBytes = (value: number) => {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

const formatRate = (value: number) => `${formatBytes(value)}/s`;

const formatPerSecond = (value: number) => `${value.toFixed(1)}/s`;

const formatMilliseconds = (value: number) => `${Math.round(value)} ms`;

const formatMillicores = (value: number) =>
  value >= 1000 ? `${(value / 1000).toFixed(1)} vCPU` : `${Math.round(value)}m`;

const statusFor = (usage: Usage | null) => {
  if (!usage) {
    return "Reading current usage…";
  }
  return usage.running ? "Live" : "Stopped";
};

const cpuValueFor = (usage: Usage | null, cpuMillicores?: number) => {
  if (!usage?.running) {
    return "—";
  }
  return cpuMillicores === undefined
    ? "Sampling…"
    : formatMillicores(cpuMillicores);
};

const networkValueFor = (
  usage: Usage | null,
  network?: { egress: number; ingress: number }
) => {
  if (network) {
    return `${formatRate(network.ingress)} ↓  ${formatRate(network.egress)} ↑`;
  }
  return usage?.running && usage.networkAvailable ? "Sampling…" : "—";
};

const emptyLabelFor = (
  history: ResourceUsageHistory | null,
  error?: string
) => {
  if (error) {
    return error;
  }
  return history ? "Collecting samples…" : "Loading history…";
};

const historyStatusFor = (
  history: ResourceUsageHistory | null,
  error?: string
) => {
  if (error) {
    return error;
  }
  const samples = history
    ? Math.max(
        history.points.length,
        ...history.series.map((item) => item.points.length)
      )
    : 0;
  return history ? `${samples} samples` : "Loading…";
};

const Metric = ({
  detail,
  label,
  value,
}: {
  detail: string;
  label: string;
  value: string;
}) => (
  <div className="border-b border-border px-4 py-3 sm:border-r sm:border-b-0 sm:last:border-r-0">
    <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
      {label}
    </p>
    <p className="mt-1 text-[10px]">{value}</p>
    <p className="mt-1 text-[9px] text-muted-foreground">{detail}</p>
  </div>
);

const UsageHeader = ({
  error,
  loading,
  state,
  title = "Resource usage",
}: {
  error?: string;
  loading: boolean;
  state: string;
  title?: string;
}) => (
  <div className="flex min-h-11 flex-wrap items-center gap-2 border-b border-border px-4 py-2.5 text-[9px] text-muted-foreground">
    {loading ? (
      <LoaderCircle className="size-3 animate-spin" />
    ) : (
      <Activity className="size-3" />
    )}
    <span className="tracking-[0.12em] uppercase">{title}</span>
    <span className="ml-auto">{error ?? state}</span>
  </div>
);

const UsageSummary = ({
  actualCPU,
  actualNetwork,
  cpuLimit,
  memoryLimit,
  usage,
  aggregate = false,
  showHostCapacity = true,
}: {
  actualCPU?: number;
  actualNetwork?: { egress: number; ingress: number };
  cpuLimit?: number;
  memoryLimit?: number;
  usage: Usage | null;
  aggregate?: boolean;
  showHostCapacity?: boolean;
}) => {
  const actualMemory = usage?.running ? formatBytes(usage.memoryBytes) : "—";
  const actualDisk =
    usage?.diskBytes === undefined ? "Scanning…" : formatBytes(usage.diskBytes);
  const resourceDetail = usage
    ? `${usage.runningResources.toLocaleString()} / ${usage.totalResources.toLocaleString()} resources running`
    : "Reading resources…";
  return (
    <div
      className={`grid sm:grid-cols-2 ${showHostCapacity ? "lg:grid-cols-5" : "lg:grid-cols-4"}`}
    >
      <Metric
        detail={
          aggregate
            ? resourceDetail
            : `Limit ${cpuLimit ? `${cpuLimit.toLocaleString()}m` : "unlimited"}`
        }
        label="CPU now"
        value={cpuValueFor(usage, actualCPU)}
      />
      <Metric
        detail={
          aggregate
            ? "All running workloads"
            : `Limit ${memoryLimit ? formatBytes(memoryLimit) : "unlimited"}`
        }
        label="Memory now"
        value={actualMemory}
      />
      <Metric detail="Persistent volumes" label="Disk now" value={actualDisk} />
      <Metric
        detail="Public ingress ↓  egress ↑"
        label="Network now"
        value={networkValueFor(usage, actualNetwork)}
      />
      {showHostCapacity ? (
        <Metric
          detail={
            usage ? `${usage.hostCpuCores.toLocaleString()} vCPU` : "Reading…"
          }
          label="Host capacity"
          value={usage ? formatBytes(usage.hostMemoryBytes) : "—"}
        />
      ) : null}
    </div>
  );
};

const RangeSelector = ({
  history,
  historyError,
  loading,
  onChange,
  range,
  standalone = false,
}: {
  history: ResourceUsageHistory | null;
  historyError?: string;
  loading?: boolean;
  onChange: (range: ResourceUsageRange) => void;
  range: ResourceUsageRange;
  standalone?: boolean;
}) => (
  <div
    className={`flex items-center gap-1 px-4 py-2.5 ${
      standalone ? "border border-border bg-card" : "border-t border-border"
    }`}
  >
    <span className="mr-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
      Range
    </span>
    {ranges.map((option) => (
      <button
        className={`h-7 border px-2.5 text-[9px] transition-colors ${
          range === option.value
            ? "border-foreground bg-foreground text-background"
            : "border-border text-muted-foreground hover:bg-muted hover:text-foreground"
        }`}
        key={option.value}
        onClick={() => onChange(option.value)}
        type="button"
      >
        {option.label}
      </button>
    ))}
    <span className="ml-auto flex items-center gap-1.5 text-[8px] text-muted-foreground">
      {loading ? <LoaderCircle className="size-3 animate-spin" /> : null}
      {loading ? `Loading ${range}…` : historyStatusFor(history, historyError)}
    </span>
  </div>
);

const UsageCharts = ({
  aggregate = false,
  history,
  historyError,
  loading = false,
}: {
  aggregate?: boolean;
  history: ResourceUsageHistory | null;
  historyError?: string;
  loading?: boolean;
}) => {
  const points = history?.points ?? emptyPoints;
  const emptyLabel = emptyLabelFor(history, historyError);
  const from = history?.from ?? 0;
  const to = history?.to ?? 0;
  const cpu = useMemo(
    () => breakdownSeriesFor(history, cpuBreakdownValue),
    [history]
  );
  const memory = useMemo(
    () => breakdownSeriesFor(history, memoryBreakdownValue),
    [history]
  );
  const disk = useMemo(
    () => breakdownSeriesFor(history, diskBreakdownValue),
    [history]
  );
  const network = useMemo(
    () => breakdownSeriesFor(history, networkBreakdownValue),
    [history]
  );
  return (
    <div
      aria-busy={loading}
      className={`grid border-t border-border transition-opacity lg:grid-cols-2 ${
        loading ? "opacity-45" : "opacity-100"
      }`}
    >
      <div className="min-w-0 lg:border-r lg:border-border">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatMillicores}
          from={from}
          minimumMaximum={100}
          points={points}
          series={aggregate ? cpu : cpuSeries}
          title="CPU"
          to={to}
        />
      </div>
      <div className="min-w-0 border-t border-border lg:border-t-0">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatBytes}
          from={from}
          minimumMaximum={1024 ** 2}
          points={points}
          series={aggregate ? memory : memorySeries}
          title="Memory"
          to={to}
        />
      </div>
      <div className="min-w-0 border-t border-border lg:border-r lg:border-border">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatBytes}
          from={from}
          minimumMaximum={1024 ** 2}
          points={points}
          series={aggregate ? disk : diskSeries}
          title="Disk usage"
          to={to}
        />
      </div>
      <div className="min-w-0 border-t border-border">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatRate}
          from={from}
          minimumMaximum={1024}
          points={points}
          series={aggregate ? network : networkSeries}
          title={
            aggregate ? "Network traffic · ingress + egress" : "Network traffic"
          }
          to={to}
        />
      </div>
    </div>
  );
};

const proxyValue = (value?: number) =>
  value === undefined ? "Sampling…" : formatPerSecond(value);

const latencyValue = (value?: number) =>
  value === undefined ? "—" : formatMilliseconds(value);

interface ProtocolUsageProps {
  aggregate?: boolean;
  history: ResourceUsageHistory | null;
  historyError?: string;
  usage: Usage | null;
}

type HTTPMetrics = NonNullable<Usage["proxy"]>["http"];

const emptyHTTPMetrics: HTTPMetrics = {
  activeRequests: 0,
  activeRequestsPeak: 0,
  requestsTotal: 0,
};

const combinedRate = (first?: number, second?: number) =>
  first === undefined || second === undefined ? undefined : first + second;

const ProtocolHeader = ({
  title,
  usage,
}: {
  title: string;
  usage: Usage | null;
}) => (
  <UsageHeader
    loading={!usage}
    state={usage?.proxy ? "Live" : "Waiting for traffic"}
    title={title}
  />
);

const HTTPUsage = ({
  aggregate = false,
  history,
  historyError,
  usage,
}: ProtocolUsageProps) => {
  const http = usage?.proxy?.http ?? emptyHTTPMetrics;
  const points = history?.points ?? emptyPoints;
  const emptyLabel = emptyLabelFor(history, historyError);
  const from = history?.from ?? 0;
  const to = history?.to ?? 0;
  const requests = useMemo(
    () => breakdownSeriesFor(history, httpRequestBreakdownValue),
    [history]
  );
  const latency = useMemo(
    () => breakdownSeriesFor(history, httpLatencyBreakdownValue),
    [history]
  );
  return (
    <section className="border border-border bg-card">
      <ProtocolHeader title="HTTP traffic" usage={usage} />
      <div className="grid sm:grid-cols-2 xl:grid-cols-4">
        <Metric
          detail={`${http.requestsTotal.toLocaleString()} total requests`}
          label="Request rate"
          value={proxyValue(http.requestsPerSecond)}
        />
        <Metric
          detail={`${http.activeRequestsPeak.toLocaleString()} peak · includes SSE/WebSocket`}
          label="Active requests"
          value={http.activeRequests.toLocaleString()}
        />
        <Metric
          detail={`p50 ${latencyValue(http.latencyP50Millis)} · p99 ${latencyValue(http.latencyP99Millis)}`}
          label="Latency p95"
          value={latencyValue(http.latencyP95Millis)}
        />
        <Metric
          detail="4xx and 5xx responses"
          label="Error rate"
          value={proxyValue(
            combinedRate(http.responses4xxPerSecond, http.responses5xxPerSecond)
          )}
        />
      </div>
      <div className="grid grid-cols-2 border-t border-border xl:grid-cols-4">
        <Metric
          detail="Successful"
          label="2xx"
          value={proxyValue(http.responses2xxPerSecond)}
        />
        <Metric
          detail="Redirects"
          label="3xx"
          value={proxyValue(http.responses3xxPerSecond)}
        />
        <Metric
          detail="Client errors"
          label="4xx"
          value={proxyValue(http.responses4xxPerSecond)}
        />
        <Metric
          detail="Server errors"
          label="5xx"
          value={proxyValue(http.responses5xxPerSecond)}
        />
      </div>
      <div className="grid border-t border-border xl:grid-cols-2">
        <div className="min-w-0 xl:border-r xl:border-border">
          <MetricChart
            emptyLabel={emptyLabel}
            formatValue={formatPerSecond}
            from={from}
            minimumMaximum={1}
            points={points}
            series={aggregate ? requests : httpRequestSeries}
            title="HTTP request rate"
            to={to}
          />
        </div>
        <div className="min-w-0 border-t border-border xl:border-t-0">
          <MetricChart
            emptyLabel={emptyLabel}
            formatValue={formatMilliseconds}
            from={from}
            minimumMaximum={10}
            points={points}
            series={aggregate ? latency : httpLatencySeries}
            title={aggregate ? "HTTP latency · p95" : "HTTP latency"}
            to={to}
          />
        </div>
      </div>
    </section>
  );
};

const TCPUsage = ({
  aggregate = false,
  history,
  historyError,
  usage,
}: ProtocolUsageProps) => {
  const proxy = usage?.proxy;
  const points = history?.points ?? emptyPoints;
  const connections = useMemo(
    () => breakdownSeriesFor(history, tcpBreakdownValue),
    [history]
  );
  return (
    <section className="border border-border bg-card">
      <ProtocolHeader title="TCP traffic" usage={usage} />
      <div className="grid sm:grid-cols-3">
        <Metric
          detail="Accepted connections per second"
          label="Connection rate"
          value={proxyValue(proxy?.tcp.connectionsPerSecond)}
        />
        <Metric
          detail={`${(proxy?.tcp.activeConnectionsPeak ?? 0).toLocaleString()} peak in latest interval`}
          label="Active connections"
          value={(proxy?.tcp.activeConnections ?? 0).toLocaleString()}
        />
        <Metric
          detail="Since the daemon started"
          label="Total connections"
          value={(proxy?.tcp.connectionsTotal ?? 0).toLocaleString()}
        />
      </div>
      <div className="border-t border-border">
        <MetricChart
          emptyLabel={emptyLabelFor(history, historyError)}
          formatValue={formatPerSecond}
          from={history?.from ?? 0}
          minimumMaximum={1}
          points={points}
          series={aggregate ? connections : tcpSeries}
          title="TCP connection rate"
          to={history?.to ?? 0}
        />
      </div>
    </section>
  );
};

const UDPUsage = ({
  aggregate = false,
  history,
  historyError,
  usage,
}: ProtocolUsageProps) => {
  const proxy = usage?.proxy;
  const points = history?.points ?? emptyPoints;
  const packets = useMemo(
    () => breakdownSeriesFor(history, udpBreakdownValue),
    [history]
  );
  return (
    <section className="border border-border bg-card">
      <ProtocolHeader title="UDP traffic" usage={usage} />
      <div className="grid sm:grid-cols-2 xl:grid-cols-4">
        <Metric
          detail="Public packets received"
          label="Ingress packets"
          value={proxyValue(proxy?.udp.ingressPacketsPerSecond)}
        />
        <Metric
          detail="Public packets sent"
          label="Egress packets"
          value={proxyValue(proxy?.udp.egressPacketsPerSecond)}
        />
        <Metric
          detail="Since the daemon started"
          label="Ingress total"
          value={(proxy?.udp.ingressPacketsTotal ?? 0).toLocaleString()}
        />
        <Metric
          detail="Since the daemon started"
          label="Egress total"
          value={(proxy?.udp.egressPacketsTotal ?? 0).toLocaleString()}
        />
      </div>
      <div className="border-t border-border">
        <MetricChart
          emptyLabel={emptyLabelFor(history, historyError)}
          formatValue={formatPerSecond}
          from={history?.from ?? 0}
          minimumMaximum={1}
          points={points}
          series={aggregate ? packets : udpSeries}
          title={
            aggregate ? "UDP packet rate · ingress + egress" : "UDP packet rate"
          }
          to={history?.to ?? 0}
        />
      </div>
    </section>
  );
};

const ProtocolUsage = (props: ProtocolUsageProps) => (
  <>
    {props.usage?.trafficRoutes.http ? <HTTPUsage {...props} /> : null}
    {props.usage?.trafficRoutes.tcp ? <TCPUsage {...props} /> : null}
    {props.usage?.trafficRoutes.udp ? <UDPUsage {...props} /> : null}
  </>
);

type HostMetrics = NonNullable<Usage["host"]>;

const emptyHostMetrics: HostMetrics = {
  cpuCores: 1,
  memoryPeakBytes: 0,
  memoryTotalBytes: 1,
  memoryUsedBytes: 0,
  networkInterface: "—",
  observedAt: 1,
};

const VPSCurrent = ({ host }: { host?: HostMetrics }) => {
  const values = host ?? emptyHostMetrics;
  const cpuPercent =
    values.cpuMillicores === undefined
      ? undefined
      : (values.cpuMillicores / (values.cpuCores * 1000)) * 100;
  const memoryPercent =
    (values.memoryUsedBytes / values.memoryTotalBytes) * 100;
  const network =
    values.networkIngressBytesPerSecond !== undefined &&
    values.networkEgressBytesPerSecond !== undefined
      ? `${formatRate(values.networkIngressBytesPerSecond)} ↓  ${formatRate(values.networkEgressBytesPerSecond)} ↑`
      : "Sampling…";
  return (
    <>
      <div className="flex min-h-11 items-center gap-2 border-b border-border px-4 py-2.5 text-[9px] text-muted-foreground">
        <Server className="size-3" />
        <span className="tracking-[0.12em] uppercase">VPS total</span>
        <span className="ml-auto">{host ? "Live" : "Reading host…"}</span>
      </div>
      <div className="grid sm:grid-cols-3">
        <Metric
          detail={
            host
              ? `${formatMillicores(values.cpuMillicores ?? 0)} / ${values.cpuCores} vCPU`
              : "Reading CPU…"
          }
          label="CPU total"
          value={
            cpuPercent === undefined ? "Sampling…" : `${cpuPercent.toFixed(1)}%`
          }
        />
        <Metric
          detail={
            host ? `${memoryPercent.toFixed(1)}% used` : "Reading memory…"
          }
          label="RAM total"
          value={
            host
              ? `${formatBytes(values.memoryUsedBytes)} / ${formatBytes(values.memoryTotalBytes)}`
              : "—"
          }
        />
        <Metric
          detail={
            host
              ? `${values.networkInterface} host traffic`
              : "Detecting uplink…"
          }
          label="Network total"
          value={network}
        />
      </div>
    </>
  );
};

const VPSCharts = ({
  cpuCores,
  history,
  historyError,
  loading = false,
}: {
  cpuCores: number;
  history: ResourceUsageHistory | null;
  historyError?: string;
  loading?: boolean;
}) => {
  const cpuSeriesForHost = useMemo<MetricSeries<UsagePoint>[]>(
    () => [
      {
        color: chartColors.primary,
        label: "Average",
        value: (point) =>
          point.cpuMillicores === undefined
            ? undefined
            : (point.cpuMillicores / (cpuCores * 1000)) * 100,
      },
      {
        color: chartColors.primary,
        label: "Peak (2s avg)",
        strokeDasharray: "3 2",
        value: (point) =>
          point.cpuPeakMillicores === undefined
            ? undefined
            : (point.cpuPeakMillicores / (cpuCores * 1000)) * 100,
      },
    ],
    [cpuCores]
  );
  const points = history?.points ?? emptyPoints;
  const emptyLabel = emptyLabelFor(history, historyError);
  const from = history?.from ?? 0;
  const to = history?.to ?? 0;
  return (
    <div
      aria-busy={loading}
      className={`grid border-t border-border transition-opacity xl:grid-cols-2 ${
        loading ? "opacity-45" : "opacity-100"
      }`}
    >
      <div className="min-w-0 xl:border-r xl:border-border">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={(value) => `${value.toFixed(1)}%`}
          from={from}
          minimumMaximum={10}
          points={points}
          series={cpuSeriesForHost}
          title="VPS CPU"
          to={to}
        />
      </div>
      <div className="min-w-0 border-t border-border xl:border-t-0">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatBytes}
          from={from}
          minimumMaximum={1024 ** 3}
          points={points}
          series={memorySeries}
          title="VPS RAM"
          to={to}
        />
      </div>
      <div className="min-w-0 border-t border-border xl:col-span-2">
        <MetricChart
          emptyLabel={emptyLabel}
          formatValue={formatRate}
          from={from}
          minimumMaximum={1024}
          points={points}
          series={networkSeries}
          title="VPS network"
          to={to}
        />
      </div>
    </div>
  );
};

const VPSUsage = ({
  history,
  historyError,
  historyLoading,
  usage,
}: {
  history: ResourceUsageHistory | null;
  historyError?: string;
  historyLoading?: boolean;
  usage: Usage | null;
}) => {
  const host = usage?.host;
  return (
    <section className="border border-border bg-card">
      <VPSCurrent host={host} />
      <VPSCharts
        cpuCores={host?.cpuCores ?? 1}
        history={history}
        historyError={historyError}
        loading={historyLoading}
      />
    </section>
  );
};

const UsageContent = ({
  aggregate = false,
  cpuLimit,
  cpuMillicores,
  currentError,
  history,
  historyError,
  historyLoading,
  memoryBytes,
  network,
  onRangeChange,
  range,
  showHostCapacity = true,
  showRange = true,
  title,
  usage,
}: {
  aggregate?: boolean;
  cpuLimit?: number;
  cpuMillicores?: number;
  currentError?: string;
  history: ResourceUsageHistory | null;
  historyError?: string;
  historyLoading?: boolean;
  memoryBytes?: number;
  network?: { egress: number; ingress: number };
  onRangeChange: (range: ResourceUsageRange) => void;
  range: ResourceUsageRange;
  showHostCapacity?: boolean;
  showRange?: boolean;
  title: string;
  usage: Usage | null;
}) => (
  <section className="border border-border bg-card">
    <UsageHeader
      error={currentError}
      loading={!usage && !currentError}
      state={statusFor(usage)}
      title={title}
    />
    <UsageSummary
      actualCPU={cpuMillicores}
      actualNetwork={network}
      aggregate={aggregate}
      cpuLimit={cpuLimit}
      memoryLimit={memoryBytes}
      showHostCapacity={showHostCapacity}
      usage={usage}
    />
    {showRange ? (
      <RangeSelector
        history={history}
        historyError={historyError}
        loading={historyLoading}
        onChange={onRangeChange}
        range={range}
      />
    ) : null}
    <UsageCharts
      aggregate={aggregate}
      history={history}
      historyError={historyError}
      loading={historyLoading}
    />
  </section>
);

export const ResourceUsage = ({
  cpuMillicores,
  kind,
  memoryBytes,
  onRangeChange,
  range: controlledRange,
  resourceID,
  showRange = true,
}: {
  cpuMillicores?: number;
  kind: ResourceUsageKind;
  memoryBytes?: number;
  onRangeChange?: (range: ResourceUsageRange) => void;
  range?: ResourceUsageRange;
  resourceID: string;
  showRange?: boolean;
}) => {
  const [localRange, setLocalRange] = useState<ResourceUsageRange>("1h");
  const range = controlledRange ?? localRange;
  const setRange = onRangeChange ?? setLocalRange;
  const {
    cpuMillicores: actualCPU,
    error: currentError,
    network: actualNetwork,
    usage,
  } = useCurrentResourceUsage(kind, resourceID);
  const {
    error: historyError,
    history,
    loading: historyLoading,
  } = useResourceUsageHistory(kind, resourceID, range);

  return (
    <div className="space-y-4">
      <UsageContent
        cpuLimit={cpuMillicores}
        cpuMillicores={actualCPU}
        currentError={currentError}
        history={history}
        historyError={historyError}
        historyLoading={historyLoading}
        memoryBytes={memoryBytes}
        network={actualNetwork}
        onRangeChange={setRange}
        range={range}
        showRange={showRange}
        title="Resource usage"
        usage={usage}
      />
      {kind === "service" || kind === "network_gateway" ? (
        <ProtocolUsage
          history={history}
          historyError={historyError}
          usage={usage}
        />
      ) : null}
    </div>
  );
};

export const ProjectUsage = ({ projectID }: { projectID: string }) => {
  const [range, setRange] = useState<ResourceUsageRange>("1h");
  const metrics = useScopeUsage({ id: projectID, kind: "project" }, range);
  return (
    <div>
      <header className="border-b border-border px-6 py-5">
        <h3 className="text-sm font-medium">Usage</h3>
        <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
          CPU, memory, persistent disk, and public traffic split by workload.
        </p>
      </header>
      <div className="space-y-4 p-4 lg:p-6">
        <UsageContent
          aggregate
          cpuMillicores={metrics.cpuMillicores}
          currentError={metrics.currentError}
          history={metrics.history}
          historyError={metrics.historyError}
          historyLoading={metrics.historyLoading}
          network={metrics.network}
          onRangeChange={setRange}
          range={range}
          title="Project resources"
          usage={metrics.usage}
        />
        <CustomMetrics scope={{ kind: "project", projectID }} />
        <ProtocolUsage
          aggregate
          history={metrics.history}
          historyError={metrics.historyError}
          usage={metrics.usage}
        />
      </div>
    </div>
  );
};

export const InstallationUsage = () => {
  const [range, setRange] = useState<ResourceUsageRange>("1h");
  const metrics = useScopeUsage({ kind: "installation" }, range);
  return (
    <main className="space-y-4 p-4 lg:p-6">
      <RangeSelector
        history={metrics.history}
        historyError={metrics.historyError}
        loading={metrics.historyLoading}
        onChange={setRange}
        range={range}
        standalone
      />
      <VPSUsage
        history={metrics.hostHistory}
        historyError={metrics.hostHistoryError}
        historyLoading={metrics.historyLoading}
        usage={metrics.usage}
      />
      <UsageContent
        aggregate
        cpuMillicores={metrics.cpuMillicores}
        currentError={metrics.currentError}
        history={metrics.history}
        historyError={metrics.historyError}
        historyLoading={metrics.historyLoading}
        network={metrics.network}
        onRangeChange={setRange}
        range={range}
        showHostCapacity={false}
        showRange={false}
        title="Platform resources"
        usage={metrics.usage}
      />
      <ProtocolUsage
        aggregate
        history={metrics.history}
        historyError={metrics.historyError}
        usage={metrics.usage}
      />
      <CustomMetrics scope={{ kind: "installation" }} />
    </main>
  );
};
