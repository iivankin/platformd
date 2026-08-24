import { formatAiCost } from "@/ai-price";
import type { AiOverview } from "@/api";
import type { MetricPoint } from "@/metric-chart";

export const milliseconds = (unixNano: string) =>
  Number(BigInt(unixNano) / 1_000_000n);

export const overviewStep = (from: number, to: number) => {
  const duration = to - from;
  if (duration <= 60 * 60_000) {
    return 60_000;
  }
  if (duration <= 24 * 60 * 60_000) {
    return 15 * 60_000;
  }
  if (duration <= 7 * 24 * 60 * 60_000) {
    return 60 * 60_000;
  }
  if (duration <= 31 * 24 * 60 * 60_000) {
    return 6 * 60 * 60_000;
  }
  return 24 * 60 * 60_000;
};

export interface AiTimelinePoint extends MetricPoint {
  agentRuns: number;
  cost: number;
  errors: number;
  estimatedCost: boolean;
  generations: number;
  hasPrice: boolean;
  inputTokens: number;
  outputTokens: number;
  partialCost: boolean;
  toolCalls: number;
}

const activityPoints = (overview: AiOverview) => {
  const points = new Map<number, AiTimelinePoint>();
  for (const point of overview.activity) {
    const observedAt = milliseconds(point.timeUnixNano);
    points.set(observedAt, {
      agentRuns: point.agentRunCount,
      cost: 0,
      errors: point.errorCount,
      estimatedCost: false,
      generations: point.generationCount,
      hasPrice: point.generationCount === 0,
      inputTokens: 0,
      observedAt,
      outputTokens: 0,
      partialCost: false,
      toolCalls: point.toolCallCount,
    });
  }
  return points;
};

export interface PricedUsage {
  cacheReadTokens: number;
  cacheWriteTokens: number;
  cost: number;
  estimatedCost: boolean;
  hasPrice: boolean;
  inputTokens: number;
  outputTokens: number;
  partialCost: boolean;
  reasoningTokens: number;
}

type CostProvenance = Pick<
  PricedUsage,
  "cost" | "estimatedCost" | "hasPrice" | "partialCost"
>;

export const formatPricedUsageCost = (
  usage: CostProvenance,
  hasUsage = true
) => {
  if (!usage.hasPrice && hasUsage) {
    return "—";
  }
  return `${formatAiCost(usage.cost, usage.estimatedCost)}${usage.partialCost ? "+" : ""}`;
};

export const emptyPricedUsage = (): PricedUsage => ({
  cacheReadTokens: 0,
  cacheWriteTokens: 0,
  cost: 0,
  estimatedCost: false,
  hasPrice: false,
  inputTokens: 0,
  outputTokens: 0,
  partialCost: false,
  reasoningTokens: 0,
});

type AiCostUsage =
  | AiOverview["usage"][number]
  | AiOverview["modelUsage"][number];
type StoredCostUsage = Pick<
  AiCostUsage,
  "estimatedCostUsd" | "reportedCostUsd"
>;

interface StoredUsagePrice {
  estimated: boolean;
  value: number;
}

const priceUsage = (usage: StoredCostUsage): StoredUsagePrice | undefined => {
  if (usage.reportedCostUsd === null && usage.estimatedCostUsd === null) {
    return;
  }
  return {
    estimated: usage.estimatedCostUsd !== null,
    value: (usage.reportedCostUsd ?? 0) + (usage.estimatedCostUsd ?? 0),
  };
};

const addUsage = (
  total: PricedUsage,
  usage: AiCostUsage,
  price: StoredUsagePrice | undefined
) => {
  total.cacheReadTokens += usage.cacheReadTokens;
  total.cacheWriteTokens += usage.cacheWriteTokens;
  total.inputTokens += usage.inputTokens;
  total.outputTokens += usage.outputTokens;
  total.reasoningTokens += usage.reasoningTokens;
  if (price) {
    total.cost += price.value;
    total.estimatedCost ||= price.estimated;
    total.hasPrice = true;
  } else {
    total.partialCost = true;
  }
};

export const overviewAnalysis = (overview: AiOverview) => {
  const points = activityPoints(overview);
  const total = emptyPricedUsage();
  const byModel = new Map<string, PricedUsage>();
  for (const usage of overview.usage) {
    const observedAt = milliseconds(usage.timeUnixNano);
    const price = priceUsage(usage);
    const point = points.get(observedAt) ?? {
      agentRuns: 0,
      cost: 0,
      errors: 0,
      estimatedCost: false,
      generations: 0,
      hasPrice: false,
      inputTokens: 0,
      observedAt,
      outputTokens: 0,
      partialCost: false,
      toolCalls: 0,
    };
    if (price) {
      point.cost += price.value * 100;
      point.estimatedCost ||= price.estimated;
      point.hasPrice = true;
    } else {
      point.partialCost = true;
    }
    point.inputTokens += usage.inputTokens;
    point.outputTokens += usage.outputTokens;
    points.set(observedAt, point);
    addUsage(total, usage, price);
  }
  for (const usage of overview.modelUsage) {
    const price = priceUsage(usage);
    const key = `${usage.provider}\u0000${usage.model}`;
    const model = byModel.get(key) ?? emptyPricedUsage();
    addUsage(model, usage, price);
    byModel.set(key, model);
  }
  return {
    byModel,
    points: [...points.values()].toSorted(
      (left, right) => left.observedAt - right.observedAt
    ),
    total,
  };
};

export interface UserRow {
  cost: number;
  estimatedCost: boolean;
  generations: number;
  hasPrice: boolean;
  partialCost: boolean;
  runs: number;
  sessions: number;
  tokens: number;
  userId: string;
}

export const formatUserCost = (user: UserRow) =>
  formatPricedUsageCost(user, user.generations > 0);

export const userRows = (overview: AiOverview): UserRow[] => {
  const users = new Map<string, UserRow>();
  for (const row of overview.users) {
    const user = users.get(row.userId) ?? {
      cost: 0,
      estimatedCost: false,
      generations: 0,
      hasPrice: false,
      partialCost: false,
      runs: 0,
      sessions: 0,
      tokens: 0,
      userId: row.userId,
    };
    // Backend repeats exact per-user statistics on each cost-provenance row.
    user.runs = Math.max(user.runs, row.runCount);
    user.sessions = Math.max(user.sessions, row.sessionCount);
    user.generations += row.generationCount;
    user.tokens += row.inputTokens + row.outputTokens;
    if (row.generationCount > 0) {
      const price = priceUsage(row);
      if (price) {
        user.cost += price.value;
        user.estimatedCost ||= price.estimated;
        user.hasPrice = true;
      } else {
        user.partialCost = true;
      }
    }
    users.set(row.userId, user);
  }
  return [...users.values()].toSorted(
    (left, right) =>
      right.tokens - left.tokens ||
      right.cost - left.cost ||
      right.runs - left.runs
  );
};
