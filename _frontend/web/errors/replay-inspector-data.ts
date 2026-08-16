import { asRecord } from "./event-context";
import { replayMarkers } from "./replay-markers";
import type { ReplayRecording } from "./types";

export interface ReplayInspectorItem {
  detail?: string;
  level?: string;
  meta?: string;
  timestamp: number;
  title: string;
}

export interface ReplayMemoryPoint {
  heapLimit?: number;
  timestamp: number;
  totalHeap?: number;
  usedHeap: number;
}

const scalar = (value: unknown) =>
  typeof value === "string" || typeof value === "number"
    ? String(value)
    : undefined;

const absoluteTimestamp = (value: unknown, fallback: number) => {
  if (typeof value === "number" && Number.isFinite(value)) {
    return value < 100_000_000_000 ? value * 1000 : value;
  }
  if (typeof value === "string") {
    const parsed = Date.parse(value);
    if (!Number.isNaN(parsed)) {
      return parsed;
    }
  }
  return fallback < 100_000_000_000 ? fallback * 1000 : fallback;
};

const printable = (value: unknown) => {
  if (typeof value === "string") {
    return value;
  }
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
};

const breadcrumbMessage = (breadcrumb: Record<string, unknown>) => {
  const data = asRecord(breadcrumb.data);
  const values = data?.arguments ?? data?.args;
  if (Array.isArray(values)) {
    return values.map(printable).join(" ");
  }
  return (
    scalar(breadcrumb.message) ??
    scalar(data?.message) ??
    scalar(data?.url) ??
    "Console message"
  );
};

const networkItem = (
  value: Record<string, unknown>,
  timestamp: number
): ReplayInspectorItem | undefined => {
  const data = asRecord(value.data);
  const method = scalar(value.method ?? data?.method);
  const url = scalar(value.url ?? data?.url ?? value.description);
  if (!url) {
    return;
  }
  const status = scalar(
    value.status_code ??
      value.statusCode ??
      data?.status_code ??
      data?.statusCode
  );
  const start = value.startTimestamp;
  const end = value.endTimestamp;
  const duration =
    typeof start === "number" && typeof end === "number" && end >= start
      ? `${Math.round((end - start) * 1000)} ms`
      : undefined;
  return {
    detail: url,
    meta: [status, duration].filter(Boolean).join(" · "),
    timestamp,
    title: method ?? "Request",
  };
};

const performanceMemory = (
  payload: Record<string, unknown>,
  timestamp: number
): ReplayMemoryPoint | undefined => {
  const data = asRecord(payload.data);
  const memory = asRecord(data?.memory) ?? asRecord(payload.memory) ?? data;
  const usedHeap = memory?.usedJSHeapSize;
  if (typeof usedHeap !== "number" || !Number.isFinite(usedHeap)) {
    return;
  }
  return {
    heapLimit:
      typeof memory?.jsHeapSizeLimit === "number"
        ? memory.jsHeapSizeLimit
        : undefined,
    timestamp,
    totalHeap:
      typeof memory?.totalJSHeapSize === "number"
        ? memory.totalJSHeapSize
        : undefined,
    usedHeap,
  };
};

const collectTraceIDs = (value: unknown, traceIDs: Set<string>) => {
  if (Array.isArray(value)) {
    for (const item of value) {
      collectTraceIDs(item, traceIDs);
    }
    return;
  }
  const record = asRecord(value);
  if (!record) {
    return;
  }
  for (const [key, item] of Object.entries(record)) {
    const normalized = key.replaceAll(/[^a-zA-Z0-9]/gu, "").toLowerCase();
    if (["traceid", "traceids"].includes(normalized)) {
      const values = Array.isArray(item) ? item : [item];
      for (const traceID of values) {
        if (typeof traceID !== "string") {
          continue;
        }
        const id = traceID.replaceAll("-", "").toLowerCase();
        if (/^[\da-f]{32}$/u.test(id)) {
          traceIDs.add(id);
        }
      }
    }
    collectTraceIDs(item, traceIDs);
  }
};

const orderByTimestamp = <T extends { timestamp: number }>(values: T[]) =>
  values.toSorted((left, right) => left.timestamp - right.timestamp);

const nativeConsoleItem = (
  event: ReplayRecording["events"][number]
): ReplayInspectorItem | undefined => {
  const data = asRecord(event.data);
  if (event.type !== 3 || data?.source !== 11) {
    return;
  }
  const values = Array.isArray(data.payload) ? data.payload : [data.payload];
  return {
    level: scalar(data.level)?.toLowerCase() ?? "log",
    timestamp: absoluteTimestamp(event.timestamp, event.timestamp),
    title:
      values
        .filter((value) => value !== undefined)
        .map(printable)
        .join(" ") || "Console message",
  };
};

const customEvent = (event: ReplayRecording["events"][number]) => {
  if (event.type !== 5) {
    return;
  }
  const data = asRecord(event.data);
  const payload = asRecord(data?.payload);
  if (!payload) {
    return;
  }
  return {
    payload,
    tag: scalar(data?.tag),
    timestamp: absoluteTimestamp(
      payload.timestamp ?? payload.startTimestamp,
      event.timestamp
    ),
  };
};

const breadcrumbItems = (
  payload: Record<string, unknown>,
  timestamp: number
) => {
  const category = (scalar(payload.category) ?? "").toLowerCase();
  const consoleItem = ["console", "logcat", "timber"].includes(category)
    ? {
        level: (scalar(payload.level) ?? "log").toLowerCase(),
        timestamp,
        title: breadcrumbMessage(payload),
      }
    : undefined;
  const isNetwork =
    ["fetch", "xhr", "http"].includes(category) ||
    category.startsWith("resource.");
  return {
    consoleItem,
    networkItem: isNetwork ? networkItem(payload, timestamp) : undefined,
  };
};

const performanceItems = (
  payload: Record<string, unknown>,
  timestamp: number
) => {
  const op = (scalar(payload.op) ?? "").toLowerCase();
  const isNetwork =
    op.startsWith("resource.") || ["fetch", "xhr", "http.client"].includes(op);
  return {
    memoryPoint:
      op === "memory" ? performanceMemory(payload, timestamp) : undefined,
    networkItem: isNetwork ? networkItem(payload, timestamp) : undefined,
  };
};

export const replayInspectorData = (recording: ReplayRecording) => {
  const activity = replayMarkers(recording).map((marker) => ({
    detail: marker.detail,
    level: marker.kind,
    timestamp: marker.timestamp,
    title: marker.label,
  }));
  const consoleItems: ReplayInspectorItem[] = [];
  const network: ReplayInspectorItem[] = [];
  const memory: ReplayMemoryPoint[] = [];
  const traceIDs = new Set(recording.traceIds);

  for (const event of recording.events) {
    collectTraceIDs(event.data, traceIDs);
    const nativeItem = nativeConsoleItem(event);
    if (nativeItem) {
      consoleItems.push(nativeItem);
      continue;
    }
    const custom = customEvent(event);
    if (!custom) {
      continue;
    }
    if (custom.tag === "breadcrumb") {
      const items = breadcrumbItems(custom.payload, custom.timestamp);
      if (items.consoleItem) {
        consoleItems.push(items.consoleItem);
      }
      if (items.networkItem) {
        network.push(items.networkItem);
      }
    }
    if (custom.tag === "performanceSpan") {
      const items = performanceItems(custom.payload, custom.timestamp);
      if (items.networkItem) {
        network.push(items.networkItem);
      }
      if (items.memoryPoint) {
        memory.push(items.memoryPoint);
      }
    }
  }
  return {
    activity: orderByTimestamp(activity),
    console: orderByTimestamp(consoleItems),
    memory: orderByTimestamp(memory),
    network: orderByTimestamp(network),
    traceIDs: [...traceIDs].toSorted(),
  };
};
