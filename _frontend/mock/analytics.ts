import {
  matchingHostnames,
  normalizeTrackerRoot,
  rootsOverlap,
} from "../web/analytics-model";
import type {
  AnalyticsChart,
  AnalyticsExperiment,
  AnalyticsFlag,
  AnalyticsFunnel,
  AnalyticsGoal,
  AnalyticsTracker,
} from "../web/api";
import {
  booleanField,
  json,
  mockError,
  noContent,
  readObject,
  stringField,
} from "./http";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

/* eslint-disable complexity, no-use-before-define */

const decorateTracker = (
  state: MockState,
  tracker: AnalyticsTracker
): AnalyticsTracker => {
  const project = state.projects.find(
    (entry) => entry.id === tracker.projectId
  );
  const matching = matchingHostnames(
    tracker.rootDomain,
    Object.values(state.domains)
      .flat()
      .filter((domain) => domain.projectId === tracker.projectId)
      .map((domain) => domain.hostname)
  );
  const slug = tracker.rootDomain.toLowerCase().replaceAll(".", "--");
  const internalHostname = `analytics-${slug}.${project?.name ?? "project"}.internal`;
  return {
    ...tracker,
    internalHostname,
    internalOfrepUrl: `http://${internalHostname}:9001`,
    matchingHostnames: matching,
  };
};

const parseWindowUnit = (value: string): AnalyticsFunnel["windowUnit"] => {
  if (value === "minute" || value === "hour" || value === "day") {
    return value;
  }
  return "day";
};

const trackerRootsConflict = (
  state: MockState,
  rootDomain: string,
  exceptID = ""
) =>
  Object.values(state.analyticsTrackers)
    .flat()
    .some(
      (existing) =>
        existing.id !== exceptID &&
        rootsOverlap(existing.rootDomain, rootDomain)
    );

const pinMockWinner = (
  flag: AnalyticsFlag,
  variant: string
): AnalyticsFlag => ({
  ...flag,
  targeting: {
    groups: [{ properties: [], rollout_percentage: 100, variant }],
  },
  updatedAt: mockNow() + 1,
  variants: flag.variants.map((entry) => ({
    ...entry,
    percentage: entry.key === variant ? 100 : 0,
  })),
});

const mockRows = (
  rows: { events?: number; label: string; visitors?: number }[]
) =>
  rows.map((row) => ({
    events: row.events ?? row.visitors ?? 0,
    label: row.label,
    visitors: row.visitors ?? row.events ?? 0,
  }));

const mockBreakdown = (dimension: string) => {
  if (dimension === "pathname") {
    return mockRows([
      { label: "/", visitors: 2100 },
      { label: "/pricing", visitors: 840 },
      { label: "/signup", visitors: 310 },
      { events: 420, label: "/checkout", visitors: 180 },
    ]);
  }
  if (
    dimension === "page" ||
    dimension === "entry" ||
    dimension === "entry_page"
  ) {
    return mockRows([
      { label: "shop.example/", visitors: 2100 },
      { label: "dash.example/", visitors: 840 },
      { label: "shop.example/pricing", visitors: 310 },
      { events: 420, label: "shop.example/checkout", visitors: 180 },
    ]);
  }
  if (dimension === "exit" || dimension === "exit_page") {
    return mockRows([
      { label: "shop.example/pricing", visitors: 620 },
      { label: "dash.example/", visitors: 410 },
      { label: "shop.example/", visitors: 180 },
    ]);
  }
  if (dimension === "browser") {
    return mockRows([
      { label: "Chrome", visitors: 2480 },
      { label: "Safari", visitors: 980 },
      { label: "Firefox", visitors: 310 },
    ]);
  }
  if (dimension === "os") {
    return mockRows([
      { label: "macOS", visitors: 1620 },
      { label: "Windows", visitors: 1480 },
      { label: "iOS", visitors: 620 },
    ]);
  }
  if (dimension === "device") {
    return mockRows([
      { label: "Desktop", visitors: 2860 },
      { label: "Mobile", visitors: 1120 },
      { label: "Tablet", visitors: 140 },
    ]);
  }
  if (dimension === "country") {
    return mockRows([
      { label: "US", visitors: 1680 },
      { label: "DE", visitors: 640 },
      { label: "GB", visitors: 420 },
      { label: "NL", visitors: 210 },
      { label: "FR", visitors: 180 },
      { label: "JP", visitors: 140 },
    ]);
  }
  if (dimension === "region") {
    return mockRows([
      { label: "California", visitors: 420 },
      { label: "Bavaria", visitors: 180 },
      { label: "England", visitors: 160 },
    ]);
  }
  if (dimension === "city") {
    return mockRows([
      { label: "Berlin", visitors: 210 },
      { label: "London", visitors: 180 },
      { label: "Amsterdam", visitors: 140 },
    ]);
  }
  if (dimension === "channel") {
    return mockRows([
      { label: "Organic Search", visitors: 980 },
      { label: "Direct", visitors: 420 },
      { label: "Referral", visitors: 210 },
    ]);
  }
  if (dimension === "utm_campaign") {
    return mockRows([
      { label: "spring-sale", visitors: 310 },
      { label: "launch", visitors: 140 },
    ]);
  }
  if (dimension === "event") {
    return mockRows([
      { events: 18_420, label: "$pageview", visitors: 4120 },
      { events: 640, label: "add_to_cart", visitors: 310 },
      { events: 210, label: "signup_completed", visitors: 180 },
    ]);
  }
  return mockRows([
    { label: "Google", visitors: 980 },
    { label: "Direct", visitors: 420 },
    { label: "chatgpt.com", visitors: 140 },
  ]);
};

const mockQuery = (report: string, dimension = ""): unknown => {
  if (report === "overview") {
    return {
      current: [
        {
          bounce_rate: 0.42,
          duration: 86,
          pageviews: 18_420,
          visitors: 4120,
          visits: 5310,
        },
      ],
      previous: [
        {
          bounce_rate: 0.45,
          duration: 80,
          pageviews: 16_100,
          visitors: 3800,
          visits: 4900,
        },
      ],
      timeseries: Array.from({ length: 28 }, (_, index) => ({
        bounce_rate: 0.48 - index * 0.002,
        duration: 70 + index,
        pageviews: 500 + index * 12,
        time: mockNow() - (27 - index) * 86_400_000,
        visitors: 120 + index * 4,
        visits: 150 + index * 5,
      })),
    };
  }
  if (report === "realtime") {
    return [{ visitors: 18 }];
  }
  if (report === "breakdown" || report === "lookup") {
    return mockBreakdown(dimension);
  }
  if (report === "funnel") {
    return [
      { level: 1, visitors: 560 },
      { level: 2, visitors: 430 },
      { level: 3, visitors: 210 },
    ];
  }
  if (report === "retention") {
    return Array.from({ length: 8 }, (_, index) => ({
      cohort: Date.UTC(2026, 6, index + 1),
      d0: 1,
      d1: 0.42,
      d14: 0.12,
      d2: 0.31,
      d21: 0.09,
      d28: 0.07,
      d3: 0.24,
      d4: 0.22,
      d5: 0.2,
      d6: 0.19,
      d7: 0.18,
      size: 400 - index * 12,
    }));
  }
  if (report === "paths") {
    return [
      { from_path: "shop.example/", to_path: "dash.example/", visitors: 420 },
      {
        from_path: "shop.example/pricing",
        to_path: "shop.example/signup",
        visitors: 180,
      },
    ];
  }
  if (report === "heatmap") {
    return [
      { hits: 40, x: 48, y: 32 },
      { hits: 22, x: 52, y: 60 },
      { hits: 12, x: 20, y: 80 },
    ];
  }
  if (report === "bots") {
    return {
      hits: [
        {
          bot_kind: "training",
          bot_name: "GPTBot",
          hits: 80,
          pathname: "/",
          visitors: 80,
        },
        {
          bot_kind: "fetch",
          bot_name: "ChatGPT-User",
          hits: 12,
          pathname: "/pricing",
          visitors: 12,
        },
      ],
      referrals: [{ label: "chatgpt.com", visitors: 140 }],
    };
  }
  if (report === "sessions") {
    return [
      {
        browser: "Chrome",
        city: "Berlin",
        country: "DE",
        device: "Desktop",
        distinct_id: "aid-mock-1",
        duration: 240,
        events: 8,
        last_seen: mockNow() - 120_000,
        os: "macOS",
        views: 5,
        visit_id: "sid-mock-1",
      },
    ];
  }
  if (report === "experiment") {
    return [
      { converted: 42, exposed: 400, variant: "false" },
      { converted: 61, exposed: 410, variant: "true" },
    ];
  }
  return [];
};

export const handleAnalyticsAPI = async (
  request: Request,
  state: MockState,
  projectID: string,
  rest: string[]
): Promise<Response | undefined> => {
  if (rest[0] !== "trackers") {
    return mockError("not_found", "Analytics route was not found", 404);
  }
  const [, trackerID, collection, itemID, action] = rest;
  if (!trackerID) {
    if (request.method === "GET") {
      return json(
        (state.analyticsTrackers[projectID] ?? []).map((tracker) =>
          decorateTracker(state, tracker)
        )
      );
    }
    if (request.method === "POST") {
      const input = await readObject(request);
      const tracker: AnalyticsTracker = {
        createdAt: mockNow(),
        id: nextMockID(state, "tracker"),
        internalHostname: "",
        internalOfrepUrl: "",
        matchingHostnames: [],
        name: stringField(input, "name", "Tracker"),
        projectId: projectID,
        rootDomain: normalizeTrackerRoot(stringField(input, "rootDomain")),
        updatedAt: mockNow(),
      };
      if (trackerRootsConflict(state, tracker.rootDomain)) {
        return mockError("analytics_conflict", "Tracker roots overlap", 409);
      }
      state.analyticsTrackers[projectID] = [
        ...(state.analyticsTrackers[projectID] ?? []),
        tracker,
      ];
      return json(decorateTracker(state, tracker), 201);
    }
    return undefined;
  }
  const trackers = state.analyticsTrackers[projectID] ?? [];
  const tracker = trackers.find((entry) => entry.id === trackerID);
  if (!tracker) {
    return mockError(
      "analytics_tracker_not_found",
      "Analytics tracker was not found",
      404
    );
  }
  if (!collection) {
    if (request.method === "GET") {
      return json(decorateTracker(state, tracker));
    }
    if (request.method === "PUT") {
      const input = await readObject(request);
      const rootDomain = normalizeTrackerRoot(
        stringField(input, "rootDomain", tracker.rootDomain)
      );
      if (trackerRootsConflict(state, rootDomain, tracker.id)) {
        return mockError("analytics_conflict", "Tracker roots overlap", 409);
      }
      tracker.name = stringField(input, "name", tracker.name);
      tracker.rootDomain = rootDomain;
      tracker.updatedAt = mockNow() + 1;
      return json(decorateTracker(state, tracker));
    }
    if (request.method === "DELETE") {
      state.analyticsTrackers[projectID] = trackers.filter(
        (entry) => entry.id !== trackerID
      );
      state.analyticsGoals[trackerID] = [];
      state.analyticsFunnels[trackerID] = [];
      state.analyticsFlags[trackerID] = [];
      state.analyticsExperiments[trackerID] = [];
      state.analyticsCharts[trackerID] = [];
      return noContent();
    }
    return undefined;
  }
  if (collection === "query" && request.method === "POST") {
    const input = await readObject(request);
    return json(
      mockQuery(
        stringField(input, "report", "overview"),
        stringField(input, "dimension")
      )
    );
  }
  if (collection === "goals") {
    if (
      itemID &&
      request.method === "DELETE" &&
      (state.analyticsExperiments[trackerID] ?? []).some(
        (entry) => entry.metric.goalId === itemID
      )
    ) {
      return mockError(
        "analytics_conflict",
        "Analytics experiment conflict",
        409
      );
    }
    return handleCollection(
      request,
      state.analyticsGoals,
      trackerID,
      itemID,
      async () => {
        const input = await readObject(request);
        const goal: AnalyticsGoal = {
          actionType:
            stringField(input, "actionType") === "event" ? "event" : "path",
          actionValue: stringField(input, "actionValue", "/"),
          createdAt: mockNow(),
          hostname: stringField(input, "hostname") || undefined,
          id: nextMockID(state, "goal"),
          name: stringField(input, "name", "Goal"),
          trackerId: trackerID,
          updatedAt: mockNow(),
        };
        return goal;
      }
    );
  }
  if (collection === "funnels") {
    return handleCollection(
      request,
      state.analyticsFunnels,
      trackerID,
      itemID,
      async () => {
        const input = await readObject(request);
        const funnel: AnalyticsFunnel = {
          createdAt: mockNow(),
          id: nextMockID(state, "funnel"),
          name: stringField(input, "name", "Funnel"),
          steps: Array.isArray(input.steps)
            ? (input.steps as AnalyticsFunnel["steps"])
            : [
                { type: "path", value: "/" },
                { type: "event", value: "signup_completed" },
              ],
          trackerId: trackerID,
          updatedAt: mockNow(),
          windowUnit: parseWindowUnit(stringField(input, "windowUnit", "day")),
          windowValue: Number(input.windowValue) || 7,
        };
        return funnel;
      }
    );
  }
  if (collection === "flags") {
    if (itemID && request.method === "DELETE") {
      const running = (state.analyticsExperiments[trackerID] ?? []).some(
        (entry) => entry.flagId === itemID && !entry.endedAt
      );
      if (running) {
        return mockError(
          "analytics_conflict",
          "analytics experiment conflict",
          409
        );
      }
    }
    const response = await handleCollection(
      request,
      state.analyticsFlags,
      trackerID,
      itemID,
      async () => {
        const input = await readObject(request);
        const flag: AnalyticsFlag = {
          createdAt: mockNow(),
          description: stringField(input, "description"),
          enabled: booleanField(input, "enabled", false),
          id: nextMockID(state, "flag"),
          key: stringField(input, "key", "flag"),
          payload: input.payload ?? null,
          targeting: input.targeting ?? { groups: [] },
          trackerId: trackerID,
          type:
            stringField(input, "type") === "multivariate"
              ? "multivariate"
              : "boolean",
          updatedAt: mockNow(),
          variants: Array.isArray(input.variants)
            ? (input.variants as AnalyticsFlag["variants"])
            : [
                { key: "false", percentage: 0 },
                { key: "true", percentage: 100 },
              ],
        };
        return flag;
      }
    );
    if (itemID && request.method === "DELETE") {
      state.analyticsExperiments[trackerID] = (
        state.analyticsExperiments[trackerID] ?? []
      ).filter((entry) => entry.flagId !== itemID);
    }
    return response;
  }
  if (collection === "experiments") {
    if (action === "stop" && request.method === "POST") {
      const experiments = state.analyticsExperiments[trackerID] ?? [];
      const experiment = experiments.find((entry) => entry.id === itemID);
      if (!experiment) {
        return mockError(
          "analytics_experiment_not_found",
          "Analytics experiment was not found",
          404
        );
      }
      experiment.endedAt = mockNow();
      experiment.updatedAt = mockNow() + 1;
      return json(experiment);
    }
    if (action === "ship" && request.method === "POST") {
      const experiments = state.analyticsExperiments[trackerID] ?? [];
      const experiment = experiments.find((entry) => entry.id === itemID);
      if (!experiment) {
        return mockError(
          "analytics_experiment_not_found",
          "Analytics experiment was not found",
          404
        );
      }
      const input = await readObject(request);
      const variant = stringField(input, "variant");
      const flag = (state.analyticsFlags[trackerID] ?? []).find(
        (entry) => entry.id === experiment.flagId
      );
      if (
        !(
          flag &&
          variant &&
          flag.variants.some((entry) => entry.key === variant)
        )
      ) {
        return mockError(
          "invalid_analytics",
          "Analytics fields are invalid",
          400
        );
      }
      Object.assign(flag, pinMockWinner(flag, variant));
      experiment.endedAt = mockNow();
      experiment.updatedAt = mockNow() + 1;
      return json(experiment);
    }
    return handleCollection(
      request,
      state.analyticsExperiments,
      trackerID,
      itemID,
      async () => {
        const input = await readObject(request);
        const experiment: AnalyticsExperiment = {
          controlVariant: stringField(input, "controlVariant", "false"),
          createdAt: mockNow(),
          flagId: stringField(input, "flagId"),
          id: nextMockID(state, "experiment"),
          metric:
            typeof input.metric === "object" && input.metric
              ? (input.metric as AnalyticsExperiment["metric"])
              : { eventName: "signup_completed" },
          startedAt: mockNow(),
          trackerId: trackerID,
          updatedAt: mockNow(),
          windowUnit: parseWindowUnit(stringField(input, "windowUnit", "day")),
          windowValue: Number(input.windowValue) || 14,
        };
        return experiment;
      }
    );
  }
  if (collection === "charts") {
    return handleCollection(
      request,
      state.analyticsCharts,
      trackerID,
      itemID,
      async () => {
        const input = await readObject(request);
        const chart: AnalyticsChart = {
          createdAt: mockNow(),
          id: nextMockID(state, "chart"),
          legend: stringField(input, "legend", "none"),
          sql: stringField(
            input,
            "sql",
            "SELECT time, count() AS value FROM analytics GROUP BY time"
          ),
          title: stringField(input, "title", "Chart"),
          trackerId: trackerID,
          unit: stringField(input, "unit") || undefined,
          updatedAt: mockNow(),
          visualization: (
            ["area", "bar", "line", "table", "value"] as const
          ).includes(
            stringField(
              input,
              "visualization",
              "line"
            ) as AnalyticsChart["visualization"]
          )
            ? (stringField(
                input,
                "visualization",
                "line"
              ) as AnalyticsChart["visualization"])
            : "line",
        };
        return chart;
      }
    );
  }
  return undefined;
};

const handleCollection = async <T extends { id: string }>(
  request: Request,
  store: Record<string, T[]>,
  trackerID: string,
  itemID: string | undefined,
  create: () => Promise<T>
): Promise<Response | undefined> => {
  if (!itemID && request.method === "GET") {
    return json(store[trackerID] ?? []);
  }
  if (!itemID && request.method === "POST") {
    const created = await create();
    store[trackerID] = [...(store[trackerID] ?? []), created];
    return json(created, 201);
  }
  if (itemID && request.method === "PUT") {
    const existing = (store[trackerID] ?? []).find(
      (entry) => entry.id === itemID
    );
    if (!existing) {
      return mockError("not_found", "Analytics item was not found", 404);
    }
    const input = await readObject(request);
    Object.assign(existing, input, {
      id: existing.id,
      updatedAt: mockNow() + 1,
    });
    return json(existing);
  }
  if (itemID && request.method === "DELETE") {
    store[trackerID] = (store[trackerID] ?? []).filter(
      (entry) => entry.id !== itemID
    );
    return noContent();
  }
  return undefined;
};
