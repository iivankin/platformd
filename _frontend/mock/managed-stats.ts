import { json, mockError } from "./http";
import { mockNow } from "./state";

const managedStatsRanges = {
  "1d": { duration: 24 * 60 * 60_000, step: 15 * 60_000 },
  "1h": { duration: 60 * 60_000, step: 60_000 },
  "30d": { duration: 30 * 24 * 60 * 60_000, step: 6 * 60 * 60_000 },
  "6h": { duration: 6 * 60 * 60_000, step: 5 * 60_000 },
  "7d": { duration: 7 * 24 * 60 * 60_000, step: 60 * 60_000 },
} as const;

type ManagedStatsKind = "object_store" | "postgres" | "redis";
type ManagedStatsRange =
  (typeof managedStatsRanges)[keyof typeof managedStatsRanges];

const stableNoise = (index: number, seed: number): number => {
  const sample = (index + 1) * (seed + 17);
  const scrambled =
    (sample * sample * 13_579) / 1_000_000 + (sample * 129_898) / 10_000;
  return (scrambled - Math.floor(scrambled)) * 2 - 1;
};

const historyWindow = (requestedRange: string | null) => {
  if (!(requestedRange && requestedRange in managedStatsRanges)) {
    return;
  }
  const range =
    managedStatsRanges[requestedRange as keyof typeof managedStatsRanges];
  const to = Math.floor(mockNow() / range.step) * range.step;
  return {
    count: Math.floor(range.duration / range.step),
    from: to - range.duration,
    range,
    to,
  };
};

const loadAt = (index: number, seed: number) =>
  Math.max(
    0.05,
    0.55 + stableNoise(index, seed) * 0.35 + stableNoise(index, seed + 9) * 0.12
  );

const postgresHistoryPoint = (
  index: number,
  seed: number,
  range: ManagedStatsRange,
  from: number
) => {
  const load = loadAt(index, seed);
  const stepSeconds = range.step / 1000;
  const queriesPerSecond = Math.max(
    1,
    42 + load * 38 + stableNoise(index, seed + 3) * 8
  );
  const transactionsPerSecond = Math.max(
    0.5,
    queriesPerSecond * 0.62 + stableNoise(index, seed + 5) * 3
  );
  const meanQueryLatencyMillis = Math.max(
    0.4,
    1.8 + load * 2.4 + stableNoise(index, seed + 7) * 0.6
  );
  const rowsReadPerSecond = Math.max(
    10,
    2200 + load * 1800 + stableNoise(index, seed + 11) * 400
  );
  const rowsWrittenPerSecond = Math.max(
    1,
    180 + load * 140 + stableNoise(index, seed + 13) * 35
  );
  const bytesReadPerSecond = Math.max(
    1024,
    48_000 + load * 62_000 + stableNoise(index, seed + 17) * 12_000
  );
  const bytesHitPerSecond = Math.max(
    4096,
    620_000 + load * 280_000 + stableNoise(index, seed + 19) * 40_000
  );
  return {
    metrics: {
      active: Math.max(1, Math.round(2 + load * 4)),
      bytesHit: Math.round(bytesHitPerSecond * stepSeconds),
      bytesHitPerSecond,
      bytesRead: Math.round(bytesReadPerSecond * stepSeconds),
      bytesReadPerSecond,
      cacheHitPercent: Math.min(
        99.8,
        Math.max(88, 96.4 + stableNoise(index, seed + 23) * 2.2)
      ),
      connections: Math.max(4, Math.round(11 + load * 6)),
      idleInTransaction: load > 0.9 ? 1 : 0,
      meanQueryLatencyMillis,
      queriesPerSecond,
      queryCount: Math.round(queriesPerSecond * stepSeconds),
      rowsRead: Math.round(rowsReadPerSecond * stepSeconds),
      rowsReadPerSecond,
      rowsWritten: Math.round(rowsWrittenPerSecond * stepSeconds),
      rowsWrittenPerSecond,
      transactionsPerSecond,
    },
    observedAt: from + (index + 1) * range.step,
  };
};

const redisHistoryPoint = (
  index: number,
  seed: number,
  range: ManagedStatsRange,
  from: number
) => {
  const load = loadAt(index, seed);
  const stepSeconds = range.step / 1000;
  const operationsPerSecond = Math.max(
    1,
    420 + load * 260 + stableNoise(index, seed + 3) * 45
  );
  const latencyP50Micros = Math.max(
    40,
    90 + load * 35 + stableNoise(index, seed + 5) * 12
  );
  const netInputBytesPerSecond = Math.max(
    1024,
    18_000 + load * 12_000 + stableNoise(index, seed + 13) * 2500
  );
  const netOutputBytesPerSecond = Math.max(
    1024,
    42_000 + load * 28_000 + stableNoise(index, seed + 17) * 5000
  );
  const getOps = operationsPerSecond * 0.52;
  const setOps = operationsPerSecond * 0.28;
  const hgetOps = operationsPerSecond * 0.12;
  const otherCommandsPerSecond = Math.max(
    0,
    operationsPerSecond - getOps - setOps - hgetOps
  );
  return {
    metrics: {
      "cmd.get": getOps,
      "cmd.hget": hgetOps,
      "cmd.set": setOps,
      commandCount: Math.round(operationsPerSecond * stepSeconds),
      connectedClients: Math.max(2, Math.round(8 + load * 6)),
      hitRatePercent: Math.min(
        99.5,
        Math.max(90, 96.1 + stableNoise(index, seed + 11) * 1.8)
      ),
      latencyP50Micros,
      latencyP95Micros: latencyP50Micros * 2.4,
      latencyP99Micros: latencyP50Micros * 4.1,
      netInputBytes: Math.round(netInputBytesPerSecond * stepSeconds),
      netInputBytesPerSecond,
      netOutputBytes: Math.round(netOutputBytesPerSecond * stepSeconds),
      netOutputBytesPerSecond,
      operationsPerSecond,
      otherCommandsPerSecond,
      usedMemoryBytes: Math.round(
        44_000_000 + load * 6_000_000 + stableNoise(index, seed + 7) * 1_200_000
      ),
    },
    observedAt: from + (index + 1) * range.step,
  };
};

const objectStoreHistoryPoint = (
  index: number,
  seed: number,
  range: ManagedStatsRange,
  from: number
) => {
  const load = loadAt(index, seed);
  const stepSeconds = range.step / 1000;
  const operationsPerSecond = Math.max(
    0.2,
    8 + load * 12 + stableNoise(index, seed + 3) * 2.4
  );
  const bytesInPerSecond = Math.max(
    512,
    24_000 + load * 40_000 + stableNoise(index, seed + 5) * 8000
  );
  const bytesOutPerSecond = Math.max(
    512,
    36_000 + load * 55_000 + stableNoise(index, seed + 7) * 10_000
  );
  const getOperationsPerSecond = operationsPerSecond * 0.48;
  const putOperationsPerSecond = operationsPerSecond * 0.26;
  const deleteOperationsPerSecond = operationsPerSecond * 0.08;
  const listOperationsPerSecond = operationsPerSecond * 0.12;
  const otherOperationsPerSecond = Math.max(
    0,
    operationsPerSecond -
      getOperationsPerSecond -
      putOperationsPerSecond -
      deleteOperationsPerSecond -
      listOperationsPerSecond
  );
  return {
    metrics: {
      bytesIn: Math.round(bytesInPerSecond * stepSeconds),
      bytesInPerSecond,
      bytesOut: Math.round(bytesOutPerSecond * stepSeconds),
      bytesOutPerSecond,
      deleteOperationsPerSecond,
      errorCount: load > 0.95 ? 1 : 0,
      getOperationsPerSecond,
      listOperationsPerSecond,
      meanLatencyMicros: Math.max(
        80,
        420 + load * 260 + stableNoise(index, seed + 11) * 60
      ),
      objectCount: Math.max(1, Math.round(18 + load * 4)),
      operationCount: Math.round(operationsPerSecond * stepSeconds),
      operationsPerSecond,
      otherOperationsPerSecond,
      putOperationsPerSecond,
      totalBytes: Math.round(
        12_000_000 + load * 2_400_000 + stableNoise(index, seed + 13) * 400_000
      ),
    },
    observedAt: from + (index + 1) * range.step,
  };
};

const historySeed = (kind: ManagedStatsKind) => {
  if (kind === "postgres") {
    return 41;
  }
  if (kind === "redis") {
    return 67;
  }
  return 89;
};

const historyTotals = (
  kind: ManagedStatsKind,
  points: { metrics: Record<string, number> }[]
) => {
  const sum = (key: string) =>
    points.reduce((total, point) => total + (point.metrics[key] ?? 0), 0);
  if (kind === "postgres") {
    return {
      bytesHit: sum("bytesHit"),
      bytesRead: sum("bytesRead"),
      queryCount: sum("queryCount"),
      rowsRead: sum("rowsRead"),
      rowsWritten: sum("rowsWritten"),
    };
  }
  if (kind === "redis") {
    return {
      commandCount: sum("commandCount"),
      netInputBytes: sum("netInputBytes"),
      netOutputBytes: sum("netOutputBytes"),
    };
  }
  return {
    bytesIn: sum("bytesIn"),
    bytesOut: sum("bytesOut"),
    errorCount: sum("errorCount"),
    operationCount: sum("operationCount"),
  };
};

export const mockManagedStatsHistory = (
  kind: ManagedStatsKind,
  requestedRange: string | null
) => {
  const window = historyWindow(requestedRange);
  if (!window) {
    return mockError(
      "invalid_managed_stats_range",
      "range must be one of 1h, 6h, 1d, 7d, or 30d"
    );
  }
  const seed = historySeed(kind);
  const points = Array.from({ length: window.count }, (_, index) => {
    if (kind === "postgres") {
      return postgresHistoryPoint(index, seed, window.range, window.from);
    }
    if (kind === "redis") {
      return redisHistoryPoint(index, seed, window.range, window.from);
    }
    return objectStoreHistoryPoint(index, seed, window.range, window.from);
  });
  return json({
    from: window.from,
    points,
    stepMillis: window.range.step,
    to: window.to,
    totals: historyTotals(kind, points),
  });
};

export const mockPostgresStats = () =>
  json({
    active: 3,
    blksHit: 48_200_000,
    blksRead: 1_240_000,
    blockSizeBytes: 8192,
    blocked: [],
    bytesHitPerSecond: 780_000,
    bytesReadPerSecond: 52_000,
    cacheHitPercent: 97.5,
    connections: 14,
    databaseSizeBytes: 268_435_456,
    idle: 9,
    idleInTransaction: 0,
    indexes: [
      {
        idxScan: 182_400,
        idxTupFetch: 176_000,
        idxTupRead: 190_200,
        index: "orders_pkey",
        schema: "public",
        sizeBytes: 1_048_576,
        sizePretty: "1024 kB",
        table: "orders",
      },
      {
        idxScan: 0,
        idxTupFetch: 0,
        idxTupRead: 0,
        index: "orders_legacy_idx",
        schema: "public",
        sizeBytes: 262_144,
        sizePretty: "256 kB",
        table: "orders",
      },
    ],
    meanQueryLatencyMillis: 2.4,
    queriesPerSecond: 58.2,
    rowsReadPerSecond: 3100,
    rowsWrittenPerSecond: 210,
    sequentialScans: [
      {
        idxScan: 12,
        idxTupFetch: 8,
        nLiveTup: 18_400,
        seqScan: 42,
        seqTupRead: 620_000,
        sizeBytes: 4_194_304,
        sizePretty: "4096 kB",
        table: "events",
      },
    ],
    sessions: [
      {
        clientAddr: "10.8.0.14",
        durationMillis: 12_400,
        pid: 4211,
        query: "SELECT * FROM orders WHERE customer_id = $1",
        state: "active",
        usename: "app",
        waitEvent: "",
      },
      {
        clientAddr: "10.8.0.21",
        durationMillis: 840_000,
        pid: 4188,
        query: "",
        state: "idle",
        usename: "app",
        waitEvent: "ClientRead",
      },
    ],
    statements: [
      {
        calls: 128_400,
        maxExecTimeMillis: 42.1,
        meanExecTimeMillis: 1.8,
        percentOfTotalTime: 38.4,
        query: "SELECT * FROM orders WHERE id = $1",
        queryId: "query-orders-by-id",
        rows: 128_400,
        sharedBlksHit: 512_000,
        sharedBlksRead: 1200,
        tempBlksRead: 0,
        tempBlksWritten: 0,
        totalExecTimeMillis: 231_120,
      },
      {
        calls: 42_100,
        maxExecTimeMillis: 18.4,
        meanExecTimeMillis: 3.2,
        percentOfTotalTime: 21.1,
        query: "UPDATE products SET stock = stock - $1 WHERE id = $2",
        queryId: "query-products-stock",
        rows: 42_100,
        sharedBlksHit: 88_000,
        sharedBlksRead: 4200,
        tempBlksRead: 0,
        tempBlksWritten: 0,
        totalExecTimeMillis: 134_720,
      },
    ],
    statementsCalls: 412_000,
    statementsTotalExecTimeMillis: 640_000,
    tables: [
      {
        dataPretty: "48 MB",
        deadRows: 1200,
        indexesPretty: "6 MB",
        lastVacuum: "2026-08-07 08:12:00+00",
        rows: 830,
        table: "orders",
        totalPretty: "54 MB",
      },
      {
        dataPretty: "12 MB",
        deadRows: 40,
        indexesPretty: "2 MB",
        lastVacuum: "2026-08-07 07:40:00+00",
        rows: 93,
        table: "customers",
        totalPretty: "14 MB",
      },
    ],
    transactionsPerSecond: 36.4,
    tupDeleted: 18_400,
    tupFetched: 9_400_000,
    tupInserted: 220_000,
    tupReturned: 41_200_000,
    tupUpdated: 640_000,
    version: "PostgreSQL 17.5",
    xactCommit: 8_420_000,
    xactRollback: 12_400,
  });

export const mockRedisStats = () =>
  json({
    aofEnabled: true,
    blockedClients: 0,
    commands: [
      {
        calls: 18_700,
        microsPerCall: 8.4,
        name: "get",
        p50Micros: 72,
        p95Micros: 180,
        p99Micros: 320,
        totalMicros: 157_080,
      },
      {
        calls: 9500,
        microsPerCall: 11,
        name: "set",
        p50Micros: 95,
        p95Micros: 240,
        p99Micros: 410,
        totalMicros: 104_500,
      },
    ],
    connectedClients: 12,
    evictedKeys: 0,
    evictionPolicy: "noeviction",
    expiredKeys: 1300,
    fragmentationRatio: 1.08,
    keyspaceHits: 19_700_000,
    keyspaceMisses: 810_000,
    keyspaces: [
      {
        averageTtlMillis: 2_160_000,
        database: "db0",
        expires: 1300,
        keys: 25_300,
      },
    ],
    latencyP50Micros: 84,
    latencyP95Micros: 210,
    latencyP99Micros: 360,
    maxMemoryBytes: 0,
    operationsPerSecond: 539,
    peakMemoryBytes: 83_330_000,
    rejectedConnections: 0,
    rssMemoryBytes: 48_600_000,
    slowlog: [
      {
        client: "10.8.0.14:48221",
        command: "keys *",
        durationMicros: 12_400,
        id: 41,
        timestampMillis: mockNow() - 90_000,
      },
    ],
    totalCommands: 141_200_000,
    totalConnections: 9500,
    totalNetInputBytes: 4_820_000_000,
    totalNetOutputBytes: 12_400_000_000,
    uptimeSeconds: 8_733_600,
    usedMemoryBytes: 46_200_000,
    version: "8.2.1",
  });
