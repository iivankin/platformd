import { json, mockError, noContent } from "./http";
import type { MockState } from "./state";
import { mockNow } from "./state";

const handleIdentity = (
  request: Request,
  state: MockState,
  segments: string[]
): Response | undefined => {
  const [root, ...rest] = segments;
  if (request.method !== "GET" || rest.length > 0) {
    return undefined;
  }
  if (root === "meta") {
    return json(state.meta);
  }
  return root === "me" ? json(state.identity) : undefined;
};

const metricRanges = {
  "1d": { duration: 24 * 60 * 60_000, step: 15 * 60_000 },
  "1h": { duration: 60 * 60_000, step: 60_000 },
  "30d": { duration: 30 * 24 * 60 * 60_000, step: 6 * 60 * 60_000 },
  "6h": { duration: 6 * 60 * 60_000, step: 5 * 60_000 },
  "7d": { duration: 7 * 24 * 60 * 60_000, step: 60 * 60_000 },
} as const;

const stableNoise = (index: number, seed: number): number => {
  const sample = (index + 1) * (seed + 17);
  const scrambled =
    (sample * sample * 13_579) / 1_000_000 + (sample * 129_898) / 10_000;
  return (scrambled - Math.floor(scrambled)) * 2 - 1;
};

const mockWorkload = (count: number, seed: number): number[] => {
  let current = stableNoise(0, seed) * 0.15;
  return Array.from({ length: count }, (_, index) => {
    const burstSignal = stableNoise(Math.floor(index / 2), seed + 47);
    const burst = burstSignal > 0.45 ? (burstSignal - 0.45) * 1.8 : 0;
    const target = stableNoise(Math.floor(index / 3), seed + 19) * 0.45 + burst;
    current += (target - current) * 0.46 + stableNoise(index, seed) * 0.13;
    return Math.max(-0.65, Math.min(1.1, current));
  });
};

const mockResourceUsageHistory = (
  requestedRange: string | null,
  resources = 1,
  publicServices = 1,
  includeProxy = true
) => {
  const range =
    requestedRange && requestedRange in metricRanges
      ? metricRanges[requestedRange as keyof typeof metricRanges]
      : metricRanges["1h"];
  const to = Math.floor(Date.now() / range.step) * range.step;
  const from = to - range.duration;
  const count = Math.floor(range.duration / range.step);
  const workload = mockWorkload(count, 19);
  return {
    from,
    points: Array.from({ length: count }, (_, index) => {
      const load = workload[index] ?? 0;
      const traffic = Math.max(
        3000,
        24_000 + load * 15_000 + stableNoise(index, 307) * 5500
      );
      const requests = Math.max(
        1,
        18 + load * 8 + stableNoise(index, 353) * 2.5
      );
      const latencyPenalty = Math.max(
        0,
        load * 12 + stableNoise(index, 401) * 2
      );
      const cpuMillicores = Math.max(
        2,
        Math.round((34 + load * 13 + stableNoise(index, 101) * 2.8) * resources)
      );
      const memoryBytes = Math.round(
        (126 + load * 4 + stableNoise(index, 211) * 1.4) * resources * 1024 ** 2
      );
      const networkEgressBytesPerSecond = Math.round(
        traffic * 0.42 * publicServices
      );
      const networkIngressBytesPerSecond = Math.round(traffic * publicServices);
      return {
        cpuMillicores,
        cpuPeakMillicores: Math.round(cpuMillicores * 1.35),
        durationMillis: range.step,
        memoryBytes,
        memoryPeakBytes: Math.round(memoryBytes * 1.08),
        networkEgressBytesPerSecond,
        networkEgressPeakBytesPerSecond: Math.round(
          networkEgressBytesPerSecond * 1.5
        ),
        networkIngressBytesPerSecond,
        networkIngressPeakBytesPerSecond: Math.round(
          networkIngressBytesPerSecond * 1.5
        ),
        observedAt: from + (index + 1) * range.step,
        proxy: includeProxy
          ? {
              http: {
                activeRequests: Math.round(requests / 3),
                activeRequestsPeak: Math.round(requests / 2),
                latencyP50Millis: 24 + latencyPenalty,
                latencyP95Millis: 86 + latencyPenalty * 3,
                latencyP99Millis: 180 + latencyPenalty * 5,
                requestsPeakPerSecond: requests * publicServices * 1.6,
                requestsPerSecond: requests * publicServices,
                requestsTotal: (index + 1) * 120 * publicServices,
                responses2xxPerSecond: requests * 0.91 * publicServices,
                responses3xxPerSecond: requests * 0.03 * publicServices,
                responses4xxPerSecond: requests * 0.045 * publicServices,
                responses5xxPerSecond: requests * 0.015 * publicServices,
              },
              tcp: {
                activeConnections: 12 * publicServices,
                activeConnectionsPeak: 18 * publicServices,
                connectionsPeakPerSecond: 4.1 * publicServices,
                connectionsPerSecond: 2.4 * publicServices,
                connectionsTotal: (index + 1) * 18 * publicServices,
              },
              udp: {
                egressPacketsPeakPerSecond: 68 * publicServices,
                egressPacketsPerSecond: 44 * publicServices,
                egressPacketsTotal: (index + 1) * 400 * publicServices,
                ingressPacketsPeakPerSecond: 80 * publicServices,
                ingressPacketsPerSecond: 52 * publicServices,
                ingressPacketsTotal: (index + 1) * 470 * publicServices,
              },
            }
          : undefined,
        running: true,
      };
    }),
    stepMillis: range.step,
    to,
  };
};

const mockCurrentUsage = (
  resources = 1,
  publicServices = 1,
  includeProxy = true,
  includeHost = false
) => ({
  cpuMillicores: 84 * resources,
  cpuPeakMillicores: 84 * resources,
  host: includeHost
    ? {
        cpuCores: 8,
        cpuMillicores: 2840,
        cpuPeakMillicores: 2840,
        memoryPeakBytes: 12_348_571_648,
        memoryTotalBytes: 34_359_738_368,
        memoryUsedBytes: 12_348_571_648,
        networkEgressBytesPerSecond: 196_608,
        networkEgressPeakBytesPerSecond: 196_608,
        networkIngressBytesPerSecond: 786_432,
        networkIngressPeakBytesPerSecond: 786_432,
        networkInterface: "eth0",
        observedAt: Date.now(),
      }
    : undefined,
  hostCpuCores: 8,
  hostMemoryBytes: 34_359_738_368,
  memoryBytes: 134_217_728 * resources,
  memoryPeakBytes: 134_217_728 * resources,
  networkAvailable: true,
  networkEgressBytesPerSecond: 8192 * publicServices,
  networkEgressPeakBytesPerSecond: 8192 * publicServices,
  networkIngressBytesPerSecond: 24_576 * publicServices,
  networkIngressPeakBytesPerSecond: 24_576 * publicServices,
  observedAt: Date.now(),
  proxy: includeProxy
    ? {
        http: {
          activeRequests: 7 * publicServices,
          activeRequestsPeak: 7 * publicServices,
          latencyP50Millis: 28,
          latencyP95Millis: 94,
          latencyP99Millis: 210,
          requestsPeakPerSecond: 24.5 * publicServices,
          requestsPerSecond: 24.5 * publicServices,
          requestsTotal: 184_220 * publicServices,
          responses2xxPerSecond: 22.3 * publicServices,
          responses3xxPerSecond: 0.7 * publicServices,
          responses4xxPerSecond: 1.1 * publicServices,
          responses5xxPerSecond: 0.4 * publicServices,
        },
        tcp: {
          activeConnections: 14 * publicServices,
          activeConnectionsPeak: 14 * publicServices,
          connectionsPeakPerSecond: 2.8 * publicServices,
          connectionsPerSecond: 2.8 * publicServices,
          connectionsTotal: 24_810 * publicServices,
        },
        udp: {
          egressPacketsPeakPerSecond: 46 * publicServices,
          egressPacketsPerSecond: 46 * publicServices,
          egressPacketsTotal: 540_200 * publicServices,
          ingressPacketsPeakPerSecond: 58 * publicServices,
          ingressPacketsPerSecond: 58 * publicServices,
          ingressPacketsTotal: 680_400 * publicServices,
        },
      }
    : undefined,
  running: resources > 0,
  runningResources: resources,
  totalResources: resources,
});

const mockHostUsageHistory = (requestedRange: string | null) => {
  const range =
    requestedRange && requestedRange in metricRanges
      ? metricRanges[requestedRange as keyof typeof metricRanges]
      : metricRanges["1h"];
  const to = Math.floor(Date.now() / range.step) * range.step;
  const from = to - range.duration;
  const count = Math.floor(range.duration / range.step);
  const workload = mockWorkload(count, 19);
  return {
    from,
    points: Array.from({ length: count }, (_, index) => {
      const load = workload[index] ?? 0;
      const cpuMillicores = Math.max(
        400,
        Math.round(2700 + load * 1100 + stableNoise(index, 521) * 180)
      );
      const memoryBytes = Math.round(
        (11.4 + load * 0.45 + stableNoise(index, 617) * 0.08) * 1024 ** 3
      );
      const networkEgressBytesPerSecond = Math.max(
        20_000,
        Math.round(190_000 + load * 180_000 + stableNoise(index, 701) * 70_000)
      );
      const networkIngressBytesPerSecond = Math.max(
        160_000,
        Math.round(720_000 + load * 650_000 + stableNoise(index, 809) * 160_000)
      );
      return {
        cpuMillicores,
        cpuPeakMillicores: Math.round(cpuMillicores * 1.25),
        durationMillis: range.step,
        memoryBytes,
        memoryPeakBytes: Math.round(memoryBytes * 1.05),
        networkEgressBytesPerSecond,
        networkEgressPeakBytesPerSecond: Math.round(
          networkEgressBytesPerSecond * 1.4
        ),
        networkIngressBytesPerSecond,
        networkIngressPeakBytesPerSecond: Math.round(
          networkIngressBytesPerSecond * 1.4
        ),
        observedAt: from + (index + 1) * range.step,
        running: true,
      };
    }),
    stepMillis: range.step,
    to,
  };
};

const handleMetricUsage = (
  request: Request,
  segments: string[],
  url: URL
): Response | undefined => {
  const [root, resource, kind, resourceID, detail, subdetail, ...rest] =
    segments;
  if (
    root !== "infrastructure" ||
    request.method !== "GET" ||
    rest.length > 0
  ) {
    return undefined;
  }
  if (resource === "usage") {
    return kind === "history"
      ? json(mockResourceUsageHistory(url.searchParams.get("range"), 4))
      : json(mockCurrentUsage(4, 1, true, true));
  }
  if (resource === "host" && kind === "usage" && resourceID === "history") {
    return json(mockHostUsageHistory(url.searchParams.get("range")));
  }
  if (resource === "projects" && kind && resourceID === "usage") {
    return detail === "history"
      ? json(mockResourceUsageHistory(url.searchParams.get("range"), 4))
      : json(mockCurrentUsage(4));
  }
  if (resource !== "resources" || !kind || !resourceID || detail !== "usage") {
    return undefined;
  }
  if (!subdetail) {
    return json(mockCurrentUsage(1, 1, kind === "service"));
  }
  return subdetail === "history"
    ? json(
        mockResourceUsageHistory(
          url.searchParams.get("range"),
          1,
          1,
          kind === "service"
        )
      )
    : undefined;
};

const handleInfrastructure = (
  request: Request,
  state: MockState,
  segments: string[],
  url: URL
): Response | undefined => {
  const [root, resource] = segments;
  if (root !== "infrastructure" || segments.length > 7) {
    return undefined;
  }
  const metricUsage = handleMetricUsage(request, segments, url);
  if (metricUsage) {
    return metricUsage;
  }
  if (request.method === "GET" && resource === "disk-pressure") {
    return json({ ...state.diskPressure, checkedAt: Date.now() });
  }
  if (request.method === "GET" && resource === "logs") {
    const limit = Math.trunc(Number(url.searchParams.get("limit") ?? "500"));
    const beforeCursor = url.searchParams.get("beforeCursor");
    const boundary = beforeCursor
      ? state.infrastructureLogs.records.findIndex(
          (record) => record.cursor === beforeCursor
        )
      : -1;
    const start = boundary < 0 ? 0 : boundary + 1;
    const records = state.infrastructureLogs.records.slice(
      start,
      start + limit
    );
    const nextCursor =
      start + records.length < state.infrastructureLogs.records.length
        ? records.at(-1)?.cursor
        : undefined;
    return json({ nextCursor, records });
  }
  if (request.method === "GET" && resource === "update") {
    return json({
      currentVersion: state.meta.version,
      latestVersion: "0.2.0-mock",
      updateAvailable: true,
      updateSupported: true,
    });
  }
  if (request.method === "POST" && resource === "update") {
    return json({
      previousVersion: state.meta.version,
      targetVersion: "0.2.0-mock",
    });
  }
  return undefined;
};

const handleAudit = (
  request: Request,
  state: MockState,
  segments: string[],
  url: URL
): Response | undefined => {
  const [root, ...rest] = segments;
  if (request.method !== "GET" || root !== "audit" || rest.length > 0) {
    return undefined;
  }
  const action = url.searchParams.get("action");
  const actorKind = url.searchParams.get("actorKind");
  const projectId = url.searchParams.get("projectId");
  const result = url.searchParams.get("result");
  return json({
    events: state.auditEvents.filter(
      (event) =>
        (!action || event.action === action) &&
        (!actorKind || event.actorKind === actorKind) &&
        (!projectId || event.projectId === projectId) &&
        (!result || event.result === result)
    ),
  });
};

const handleOperations = (
  request: Request,
  state: MockState,
  segments: string[]
): Response | undefined => {
  const [root, operationID, ...rest] = segments;
  if (
    request.method !== "GET" ||
    root !== "operations" ||
    !operationID ||
    rest.length > 0
  ) {
    return undefined;
  }
  return state.operations[operationID]
    ? json(state.operations[operationID])
    : mockError("not_found", "Operation not found", 404);
};

const handleServerAndRecovery = (
  request: Request,
  segments: string[]
): Response | undefined => {
  const [root, action, ...rest] = segments;
  if (rest.length > 0) {
    return undefined;
  }
  if (
    request.method === "POST" &&
    root === "server" &&
    action === "terminal-token"
  ) {
    return json({
      expiresAt: mockNow() + 300_000,
      token: "mock-terminal-token",
    });
  }
  if (request.method === "GET" && root === "recovery" && !action) {
    return json({ resources: [] });
  }
  if (request.method === "POST" && root === "recovery" && action === "retry") {
    return noContent();
  }
  return undefined;
};

export const handleSystemAPI = (
  request: Request,
  state: MockState,
  segments: string[],
  url: URL
): Response | undefined =>
  handleIdentity(request, state, segments) ??
  handleInfrastructure(request, state, segments, url) ??
  handleAudit(request, state, segments, url) ??
  handleOperations(request, state, segments) ??
  handleServerAndRecovery(request, segments);
