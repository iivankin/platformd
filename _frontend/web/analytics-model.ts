import { z } from "zod";

import type { AnalyticsFilter, AnalyticsFunnelStep } from "@/api";

export const analyticsFilterOperatorValues = [
  "is",
  "is_not",
  "contains",
  "does_not_contain",
] as const;

export const analyticsFilterSchema = z.object({
  dimension: z.string().min(1).max(64),
  operator: z.enum(analyticsFilterOperatorValues),
  value: z.union([z.string().max(512), z.array(z.string().max(512)).max(20)]),
});

export const analyticsFiltersSchema = z.array(analyticsFilterSchema).max(8);

export const analyticsFilterGroups: {
  dimensions: { dimension: string; label: string }[];
  label: string;
}[] = [
  {
    dimensions: [
      { dimension: "page", label: "Page" },
      { dimension: "entry_page", label: "Entry page" },
      { dimension: "exit_page", label: "Exit page" },
      { dimension: "hostname", label: "Hostname" },
    ],
    label: "URL",
  },
  {
    dimensions: [
      { dimension: "source", label: "Source" },
      { dimension: "channel", label: "Channel" },
      { dimension: "referrer", label: "Referrer" },
      { dimension: "utm_medium", label: "UTM medium" },
      { dimension: "utm_source", label: "UTM source" },
      { dimension: "utm_campaign", label: "UTM campaign" },
      { dimension: "utm_content", label: "UTM content" },
      { dimension: "utm_term", label: "UTM term" },
    ],
    label: "Acquisition",
  },
  {
    dimensions: [
      { dimension: "country", label: "Country" },
      { dimension: "region", label: "Region" },
      { dimension: "city", label: "City" },
      { dimension: "browser", label: "Browser" },
      { dimension: "os", label: "OS" },
      { dimension: "device", label: "Device" },
    ],
    label: "Device",
  },
  {
    dimensions: [{ dimension: "event", label: "Event" }],
    label: "Behaviour",
  },
];

export const analyticsFilterLabel = (dimension: string) =>
  analyticsFilterGroups
    .flatMap((group) => group.dimensions)
    .find((entry) => entry.dimension === dimension)?.label ?? dimension;

export const analyticsOperatorLabel = (
  operator: AnalyticsFilter["operator"]
) => {
  if (operator === "is_not") {
    return "is not";
  }
  if (operator === "does_not_contain") {
    return "does not contain";
  }
  return operator;
};

export const analyticsFilterChip = (filter: AnalyticsFilter) => {
  const value = Array.isArray(filter.value)
    ? filter.value.join(", ")
    : filter.value;
  return `${analyticsFilterLabel(filter.dimension)} ${analyticsOperatorLabel(filter.operator)} ${value}`;
};

const ipAddress = /^(?:\d{1,3}(?:\.\d{1,3}){3}|[0-9a-f:]+)$/iu;

export const normalizeTrackerRoot = (value: string) => {
  let host = value.trim().toLowerCase().replace(/\.+$/u, "");
  if (host.startsWith("[")) {
    const end = host.indexOf("]");
    if (end > 0) {
      return host.slice(1, end);
    }
  }
  const colon = host.lastIndexOf(":");
  if (colon > 0 && /^\d+$/u.test(host.slice(colon + 1))) {
    host = host.slice(0, colon).replace(/\.+$/u, "");
  }
  return host;
};

export const matchingHostnames = (root: string, hostnames: string[]) => {
  const normalizedRoot = normalizeTrackerRoot(root);
  if (!normalizedRoot) {
    return [];
  }
  return hostnames.filter((hostname) => {
    const host = normalizeTrackerRoot(hostname);
    return host === normalizedRoot || host.endsWith(`.${normalizedRoot}`);
  });
};

export const trackerRootCandidates = (hostnames: string[]) => {
  const roots = new Set<string>();
  for (const hostname of hostnames) {
    const labels = hostname.toLowerCase().split(".").filter(Boolean);
    if (labels.length === 1) {
      roots.add(labels[0] ?? hostname.toLowerCase());
      continue;
    }
    for (let index = 0; index < labels.length - 1; index += 1) {
      roots.add(labels.slice(index).join("."));
    }
  }
  return [...roots].toSorted((left, right) =>
    left.length === right.length
      ? left.localeCompare(right)
      : left.length - right.length
  );
};

export const rootsOverlap = (left: string, right: string) => {
  const normalizedLeft = normalizeTrackerRoot(left);
  const normalizedRight = normalizeTrackerRoot(right);
  if (!(normalizedLeft && normalizedRight)) {
    return false;
  }
  return (
    normalizedLeft === normalizedRight ||
    normalizedLeft.endsWith(`.${normalizedRight}`) ||
    normalizedRight.endsWith(`.${normalizedLeft}`)
  );
};

export const analyticsCookieDomain = (root: string) => {
  const host = normalizeTrackerRoot(root);
  if (!host || !host.includes(".") || ipAddress.test(host)) {
    return "";
  }
  return `.${host}`;
};

export const asNumber = (value: unknown, fallback = 0) => {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
};

export const asString = (value: unknown, fallback = "") => {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" && Number.isFinite(value)) {
    return String(value);
  }
  return fallback;
};

export const formatCount = (value: number) =>
  new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 }).format(
    Math.round(value)
  );

export const formatPercent = (value: number) =>
  `${(value * 100).toFixed(value >= 0.1 || value === 0 ? 0 : 1)}%`;

export const formatDuration = (seconds: number) => {
  const total = Math.max(0, Math.round(seconds));
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const rest = total % 60;
  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${String(rest).padStart(2, "0")}s`;
  }
  return `${rest}s`;
};

export const deltaTone = (delta: number) => {
  if (delta > 0) {
    return "text-emerald-600";
  }
  if (delta < 0) {
    return "text-destructive";
  }
  return "text-muted-foreground";
};

export const formatDelta = (
  current: number,
  previous: number,
  invert = false
) => {
  if (previous === 0) {
    return current === 0 ? 0 : 1;
  }
  const delta = (current - previous) / previous;
  return invert ? -delta : delta;
};

export const wilsonInterval = (converted: number, exposed: number) => {
  if (exposed <= 0) {
    return { high: 0, low: 0, rate: 0 };
  }
  const zScore = 1.96;
  const n = exposed;
  const p = converted / n;
  const z2 = zScore * zScore;
  const denom = 1 + z2 / n;
  const center = (p + z2 / (2 * n)) / denom;
  const margin = (zScore * Math.sqrt((p * (1 - p) + z2 / (4 * n)) / n)) / denom;
  return {
    high: Math.min(1, center + margin),
    low: Math.max(0, center - margin),
    rate: p,
  };
};

export const funnelReached = (
  rows: { level: number; visitors: number }[],
  stepCount: number
) => {
  const byLevel = new Map(rows.map((row) => [row.level, row.visitors]));
  const total = rows.reduce((sum, row) => sum + row.visitors, 0);
  return Array.from({ length: stepCount }, (_, index) => {
    const level = index + 1;
    let reached = 0;
    for (const [entryLevel, visitors] of byLevel) {
      if (entryLevel >= level) {
        reached += visitors;
      }
    }
    return { reached, total, visitors: reached };
  });
};

export const retentionDays = [0, 1, 2, 3, 4, 5, 6, 7, 14, 21, 28] as const;

export const viewportBuckets = [320, 375, 425, 768, 1024, 1440, 1920] as const;

export const emptyFunnelSteps = (): AnalyticsFunnelStep[] => [
  { type: "path", value: "/" },
  { type: "event", value: "signup_completed" },
];

export const recordRows = (value: unknown): Record<string, unknown>[] => {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter(
    (entry): entry is Record<string, unknown> =>
      typeof entry === "object" && entry !== null
  );
};

export const firstRecord = (value: unknown): Record<string, unknown> => {
  const rows = recordRows(value);
  return rows[0] ?? {};
};

export const isAbortError = (error: unknown) =>
  typeof error === "object" &&
  error !== null &&
  "name" in error &&
  error.name === "AbortError";

export interface AnalyticsRolloutStep {
  at: number;
  percentage: number;
}

export interface AnalyticsTargetingGroup {
  properties: { key: string; operator: string; value: string }[];
  rollout_percentage: number;
  rollout_steps: AnalyticsRolloutStep[];
  variant: string | null;
}

const asObject = (value: unknown): Record<string, unknown> | undefined =>
  typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;

export const analyticsTargetingGroups = (
  targeting: unknown
): AnalyticsTargetingGroup[] => {
  const record = asObject(targeting);
  if (!record || !Array.isArray(record.groups)) {
    return [];
  }
  return record.groups.flatMap((entry) => {
    const group = asObject(entry);
    if (!group) {
      return [];
    }
    const properties = Array.isArray(group.properties)
      ? group.properties.flatMap((row) => {
          const property = asObject(row);
          if (!property) {
            return [];
          }
          return [
            {
              key: String(property.key ?? ""),
              operator: String(property.operator ?? "exact"),
              value: String(property.value ?? ""),
            },
          ];
        })
      : [];
    const steps = Array.isArray(group.rollout_steps)
      ? group.rollout_steps.flatMap((row) => {
          const step = asObject(row);
          if (!step) {
            return [];
          }
          const at = Number(step.at);
          const percentage = Number(step.percentage);
          if (!(Number.isFinite(at) && Number.isFinite(percentage))) {
            return [];
          }
          return [
            {
              at,
              percentage: Math.min(100, Math.max(0, percentage)),
            },
          ];
        })
      : [];
    return [
      {
        properties,
        rollout_percentage: Number(group.rollout_percentage) || 0,
        rollout_steps: steps.toSorted((left, right) => left.at - right.at),
        variant:
          typeof group.variant === "string" && group.variant !== ""
            ? group.variant
            : null,
      },
    ];
  });
};

export const analyticsGroupRollout = (
  group: Pick<AnalyticsTargetingGroup, "rollout_percentage" | "rollout_steps">,
  now = Date.now()
) => {
  let percentage = group.rollout_percentage;
  let found = false;
  let latest = 0;
  for (const step of group.rollout_steps) {
    if (step.at <= now && (!found || step.at >= latest)) {
      found = true;
      latest = step.at;
      ({ percentage } = step);
    }
  }
  return Math.min(100, Math.max(0, percentage));
};

const defaultTargetingGroup = (
  targeting: unknown
): AnalyticsTargetingGroup | undefined => {
  const groups = analyticsTargetingGroups(targeting);
  return groups.find((group) => group.properties.length === 0) ?? groups[0];
};

export const analyticsEffectiveRollout = (
  targeting: unknown,
  now = Date.now()
) => {
  const group = defaultTargetingGroup(targeting);
  return group ? analyticsGroupRollout(group, now) : 100;
};

export const analyticsNextRolloutStep = (
  targeting: unknown,
  now = Date.now()
) => {
  const group = defaultTargetingGroup(targeting);
  return group?.rollout_steps.find((step) => step.at > now);
};

const rolloutDay = new Intl.DateTimeFormat(undefined, {
  day: "numeric",
  month: "short",
});

export const analyticsFlagSummary = (
  flag: {
    enabled: boolean;
    targeting: unknown;
    type: string;
    variants: { key: string; percentage: number }[];
  },
  now = Date.now()
) => {
  const parts = [flag.type, flag.enabled ? "on" : "off"];
  if (flag.type === "multivariate") {
    parts.push(
      flag.variants
        .map((variant) => `${variant.key} ${variant.percentage}%`)
        .join(" / ")
    );
  }
  const rollout = analyticsEffectiveRollout(flag.targeting, now);
  const next = analyticsNextRolloutStep(flag.targeting, now);
  if (next) {
    parts.push(
      `${rollout}% → ${next.percentage}% ${rolloutDay.format(next.at)}`
    );
  } else {
    parts.push(`${rollout}%`);
  }
  return parts.join(" · ");
};

export const analyticsRampSteps = (
  from = Date.now()
): AnalyticsRolloutStep[] => [
  { at: from, percentage: 1 },
  { at: from + 86_400_000, percentage: 10 },
  { at: from + 3 * 86_400_000, percentage: 50 },
  { at: from + 7 * 86_400_000, percentage: 100 },
];

export const booleanFlagVariants = () => [
  { key: "false", percentage: 0 },
  { key: "true", percentage: 100 },
];
