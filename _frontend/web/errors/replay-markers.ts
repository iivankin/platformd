import { asRecord } from "./event-context";
import type { ReplayRecording } from "./types";

export type ReplayMarkerKind =
  | "console"
  | "error"
  | "interaction"
  | "navigation"
  | "network"
  | "warning";

export interface ReplayMarker {
  detail?: string;
  eventId?: string;
  kind: ReplayMarkerKind;
  label: string;
  timestamp: number;
}

interface RankedMarker extends ReplayMarker {
  category?: string;
  fidelity: number;
  nodeId?: number;
  source: "breadcrumb" | "error" | "native" | "span";
}

interface MarkerPresentation {
  kind: ReplayMarkerKind;
  label: string;
}

const absoluteTimestamp = (
  value: unknown,
  fallback?: number
): number | undefined => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value < 100_000_000_000 ? value * 1000 : value;
  }
  if (typeof value === "string") {
    const parsed = Date.parse(value);
    return Number.isNaN(parsed) ? fallback : parsed;
  }
  return fallback;
};

const scalar = (value: unknown) =>
  typeof value === "string" || typeof value === "number"
    ? String(value)
    : undefined;

const deviceBreadcrumbDetail = (
  category: string,
  data: Record<string, unknown> | undefined
) => {
  if (category === "device.battery") {
    const level =
      typeof data?.level === "number" ? Math.round(data.level) : "—";
    const charging = data?.charging === true ? "charging" : "not charging";
    return `Device was at ${level}% battery and ${charging}`;
  }
  if (category === "device.connectivity") {
    const state = scalar(data?.state);
    return (
      {
        cellular: "Device connected to cellular network",
        ethernet: "Device connected to ethernet",
        offline: "Internet connection was lost",
        wifi: "Device connected to wifi",
      }[state ?? ""] ?? state
    );
  }
  if (category === "device.orientation") {
    const position = scalar(data?.position);
    return position ? `Device orientation changed to ${position}` : undefined;
  }
};

const breadcrumbDetail = (
  breadcrumb: Record<string, unknown>,
  category: string
) => {
  const data = asRecord(breadcrumb.data);
  const device = deviceBreadcrumbDetail(category, data);
  if (device !== undefined) {
    return device;
  }
  const from = scalar(data?.from);
  const to = scalar(data?.to);
  if (from || to) {
    return `${from ?? "—"} → ${to ?? "—"}`;
  }
  const method = scalar(data?.method);
  const url = scalar(data?.url);
  const status = scalar(data?.status_code ?? data?.statusCode);
  if (url) {
    return [method, url, status].filter(Boolean).join(" · ");
  }
  return scalar(breadcrumb.message) ?? scalar(data?.component);
};

const specialBreadcrumbPresentation = (
  category: string
): MarkerPresentation | undefined => {
  if (category === "app.foreground") {
    return { kind: "interaction", label: "App in foreground" };
  }
  if (category === "app.background") {
    return { kind: "interaction", label: "App in background" };
  }
  if (category.startsWith("device.")) {
    const label = {
      "device.battery": "Device battery",
      "device.connectivity": "Device connectivity",
      "device.orientation": "Device orientation",
    }[category];
    return { kind: "console", label: label ?? "Device state" };
  }
  if (category === "feedback") {
    return { kind: "console", label: "User feedback" };
  }
  if (category === "replay.hydrate-error") {
    return { kind: "warning", label: "Hydration error" };
  }
  if (category === "replay.mutations") {
    return { kind: "warning", label: "Large DOM mutation" };
  }
};

const breadcrumbPresentation = (
  category: string,
  level: string,
  message?: string
): MarkerPresentation => {
  if (
    ["slowclick", "deadclick", "rageclick", "hydrate"].some((part) =>
      category.includes(part)
    )
  ) {
    return { kind: "warning", label: "Suspicious interaction" };
  }
  if (["issue", "sentry.event"].includes(category)) {
    return { kind: "error", label: message ?? "Error" };
  }
  if (category === "navigation" || category.startsWith("navigation.")) {
    return { kind: "navigation", label: "Navigation" };
  }
  const special = specialBreadcrumbPresentation(category);
  if (special) {
    return special;
  }
  if (category.startsWith("ui.")) {
    let label = "User interaction";
    if (category.includes("click")) {
      label = "User click";
    } else if (category.includes("input")) {
      label = "User input";
    }
    return { kind: "interaction", label };
  }
  if (
    ["fetch", "xhr", "http"].includes(category) ||
    category.startsWith("resource.")
  ) {
    return { kind: "network", label: "Network request" };
  }
  if (["console", "logcat", "timber"].includes(category)) {
    if (level === "error" || level === "fatal") {
      return { kind: "error", label: "Console error" };
    }
    return { kind: "console", label: "Console message" };
  }
  return {
    kind: level === "warning" ? "warning" : "console",
    label: message ?? category,
  };
};

const breadcrumbMarker = (
  payload: unknown,
  fallbackTimestamp: number
): RankedMarker | undefined => {
  const breadcrumb = asRecord(payload);
  if (!breadcrumb) {
    return;
  }
  const timestamp = absoluteTimestamp(breadcrumb.timestamp, fallbackTimestamp);
  if (timestamp === undefined) {
    return;
  }
  const category = (scalar(breadcrumb.category) ?? "default").toLowerCase();
  const level = (scalar(breadcrumb.level) ?? "info").toLowerCase();
  const message = scalar(breadcrumb.message);
  const data = asRecord(breadcrumb.data);
  const detail = breadcrumbDetail(breadcrumb, category);
  const presentation = breadcrumbPresentation(category, level, message);
  return {
    category,
    detail: presentation.kind === "network" ? (detail ?? message) : detail,
    fidelity: 2,
    nodeId: typeof data?.nodeId === "number" ? data.nodeId : undefined,
    source: "breadcrumb",
    ...presentation,
    timestamp,
  };
};

const performanceMarker = (
  payload: unknown,
  fallbackTimestamp: number
): RankedMarker | undefined => {
  const span = asRecord(payload);
  if (!span) {
    return;
  }
  const timestamp = absoluteTimestamp(span.startTimestamp, fallbackTimestamp);
  if (timestamp === undefined) {
    return;
  }
  const op = (scalar(span.op) ?? "").toLowerCase();
  const detail = scalar(span.description) ?? breadcrumbDetail(span, op);
  if (detail === "first-input-delay") {
    return;
  }
  const webVital = {
    "cumulative-layout-shift": "Cumulative Layout Shift",
    "interaction-to-next-paint": "Interaction to Next Paint",
    "largest-contentful-paint": "Largest Contentful Paint",
  }[detail ?? ""];
  if (webVital) {
    return {
      category: `web-vital:${detail}`,
      detail,
      fidelity: 2,
      kind: "console",
      label: `Web vital · ${webVital}`,
      source: "span",
      timestamp,
    };
  }
  if (op.startsWith("navigation")) {
    return {
      detail,
      fidelity: 2,
      kind: "navigation",
      label: "Navigation span",
      source: "span",
      timestamp,
    };
  }
  if (
    op.startsWith("resource.") ||
    op === "http.client" ||
    op === "fetch" ||
    op === "xhr"
  ) {
    return {
      detail,
      fidelity: 2,
      kind: "network",
      label: "Network request",
      source: "span",
      timestamp,
    };
  }
  const kind =
    op.includes("long-task") || op.includes("frozen") ? "warning" : "console";
  return {
    detail,
    fidelity: 2,
    kind,
    label: kind === "warning" ? "Performance problem" : "Performance span",
    source: "span",
    timestamp,
  };
};

const nativeMarker = (
  event: ReplayRecording["events"][number]
): RankedMarker | undefined => {
  const data = asRecord(event.data);
  if (event.type === 4) {
    return {
      detail: scalar(data?.href),
      fidelity: 1,
      kind: "navigation",
      label: "Page load",
      source: "native",
      timestamp: event.timestamp,
    };
  }
  if (event.type !== 3) {
    return;
  }
  if (data?.source === 2 && (data.type === 2 || data.type === 4)) {
    return {
      fidelity: 1,
      kind: "interaction",
      label: data.type === 4 ? "Double click" : "User click",
      source: "native",
      timestamp: event.timestamp,
    };
  }
  if (data?.source === 5 && data.userTriggered !== false) {
    return {
      fidelity: 1,
      kind: "interaction",
      label: "User input",
      source: "native",
      timestamp: event.timestamp,
    };
  }
  if (data?.source === 11) {
    const level = (scalar(data.level) ?? "log").toLowerCase();
    const kind = level === "error" || level === "fatal" ? "error" : "console";
    return {
      fidelity: 1,
      kind,
      label: kind === "error" ? "Console error" : "Console message",
      source: "native",
      timestamp: event.timestamp,
    };
  }
};

const customMarker = (
  event: ReplayRecording["events"][number]
): RankedMarker | undefined => {
  if (event.type !== 5) {
    return;
  }
  const data = asRecord(event.data);
  if (data?.tag === "breadcrumb") {
    return breadcrumbMarker(data.payload, event.timestamp);
  }
  if (data?.tag === "performanceSpan") {
    return performanceMarker(data.payload, event.timestamp);
  }
};

const addMarker = (markers: RankedMarker[], candidate?: RankedMarker) => {
  if (!candidate) {
    return;
  }
  const isSlowClick = candidate.category?.includes("slowclick") ?? false;
  const duplicate = markers.findIndex((marker) => {
    if (candidate.eventId && marker.eventId) {
      return candidate.eventId === marker.eventId;
    }
    if (
      isSlowClick &&
      marker.category?.includes("click") &&
      marker.nodeId === candidate.nodeId
    ) {
      return marker.timestamp === candidate.timestamp;
    }
    if (
      candidate.category === "web-vital:cumulative-layout-shift" &&
      marker.category === candidate.category
    ) {
      return marker.timestamp === candidate.timestamp;
    }
    const navigationPair =
      candidate.kind === "navigation" &&
      marker.kind === "navigation" &&
      new Set([candidate.source, marker.source]).has("breadcrumb") &&
      new Set([candidate.source, marker.source]).has("span");
    if (navigationPair) {
      return Math.abs(marker.timestamp - candidate.timestamp) <= 2;
    }
    return (
      marker.source !== candidate.source &&
      marker.kind === candidate.kind &&
      marker.label === candidate.label &&
      (marker.detail === candidate.detail ||
        candidate.kind === "interaction") &&
      Math.abs(marker.timestamp - candidate.timestamp) <= 2
    );
  });
  if (duplicate === -1) {
    markers.push(candidate);
  } else if (candidate.fidelity > (markers[duplicate]?.fidelity ?? 0)) {
    markers[duplicate] = candidate;
  }
};

export const replayMarkers = (recording: ReplayRecording): ReplayMarker[] => {
  const markers: RankedMarker[] = [];
  for (const event of recording.events) {
    addMarker(markers, customMarker(event));
    addMarker(markers, nativeMarker(event));
  }
  for (const event of recording.errorEvents) {
    const timestamp = absoluteTimestamp(event.timestamp);
    if (timestamp === undefined) {
      continue;
    }
    addMarker(markers, {
      detail: event.title ?? event.message,
      eventId: event.event_id,
      fidelity: 3,
      kind: event.level === "warning" ? "warning" : "error",
      label: event.title ?? "Error",
      source: "error",
      timestamp,
    });
  }
  return markers
    .toSorted((left, right) => left.timestamp - right.timestamp)
    .map(
      ({
        category: _category,
        fidelity: _fidelity,
        nodeId: _nodeId,
        source: _source,
        ...marker
      }) => marker
    );
};
