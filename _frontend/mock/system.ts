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

const mockResourceUsageHistory = (requestedRange: string | null) => {
  const range =
    requestedRange && requestedRange in metricRanges
      ? metricRanges[requestedRange as keyof typeof metricRanges]
      : metricRanges["1h"];
  const to = Math.floor(Date.now() / range.step) * range.step;
  const from = to - range.duration;
  const count = Math.floor(range.duration / range.step);
  return {
    from,
    points: Array.from({ length: count }, (_, index) => {
      const wave = Math.sin(index / 5) * 8 + Math.sin(index / 17) * 4;
      const traffic = Math.max(0, Math.sin(index / 4) * 18_000 + 24_000);
      return {
        cpuMillicores: Math.max(2, Math.round(34 + wave)),
        memoryBytes: Math.round((126 + Math.sin(index / 11) * 7) * 1024 ** 2),
        networkEgressBytesPerSecond: Math.round(traffic * 0.42),
        networkIngressBytesPerSecond: Math.round(traffic),
        observedAt: from + (index + 1) * range.step,
        running: true,
      };
    }),
    stepMillis: range.step,
    to,
  };
};

const handleInfrastructure = (
  request: Request,
  state: MockState,
  segments: string[],
  url: URL
): Response | undefined => {
  const [root, resource, kind, resourceID, detail, subdetail, ...rest] =
    segments;
  if (root !== "infrastructure" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && resource === "disk-pressure") {
    return json({ ...state.diskPressure, checkedAt: Date.now() });
  }
  if (request.method === "GET" && resource === "logs") {
    return json(state.infrastructureLogs);
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
  if (
    request.method !== "GET" ||
    resource !== "resources" ||
    !kind ||
    !resourceID ||
    detail !== "usage"
  ) {
    return undefined;
  }
  if (!subdetail) {
    return json({
      cpuUsageMicros: 4_200_000,
      hostCpuCores: 8,
      hostMemoryBytes: 34_359_738_368,
      memoryBytes: 134_217_728,
      networkAvailable: true,
      networkRxBytes: 4_800_000,
      networkTxBytes: 2_400_000,
      observedAt: Date.now(),
      running: true,
    });
  }
  if (subdetail !== "history") {
    return undefined;
  }
  return json(mockResourceUsageHistory(url.searchParams.get("range")));
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
  const result = url.searchParams.get("result");
  return json({
    events: state.auditEvents.filter(
      (event) =>
        (!action || event.action === action) &&
        (!actorKind || event.actorKind === actorKind) &&
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
