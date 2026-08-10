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
  fidelity: number;
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

const breadcrumbDetail = (breadcrumb: Record<string, unknown>) => {
  const data = asRecord(breadcrumb.data);
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
  if (category.startsWith("ui.") || category.startsWith("app.")) {
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
  if (category === "console") {
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
  const detail = breadcrumbDetail(breadcrumb);
  const presentation = breadcrumbPresentation(category, level, message);
  return {
    detail: presentation.kind === "network" ? (detail ?? message) : detail,
    fidelity: 2,
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
  const detail = scalar(span.description) ?? breadcrumbDetail(span);
  if (op.startsWith("navigation")) {
    return {
      detail,
      fidelity: 2,
      kind: "navigation",
      label: "Navigation span",
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
      timestamp: event.timestamp,
    };
  }
  if (data?.source === 5 && data.userTriggered !== false) {
    return {
      fidelity: 1,
      kind: "interaction",
      label: "User input",
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
  const duplicate = markers.findIndex(
    (marker) =>
      marker.kind === candidate.kind &&
      Math.abs(marker.timestamp - candidate.timestamp) <= 600
  );
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
      timestamp,
    });
  }
  return markers
    .toSorted((left, right) => left.timestamp - right.timestamp)
    .map(({ fidelity: _fidelity, ...marker }) => marker);
};
