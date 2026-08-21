import type {
  Service,
  ServiceMetricChart,
  ServiceTelemetry,
  ServiceTraceDetail,
  ServiceTraceSummary,
} from "../web/api";
import type { CreatedWebhook, WebhookEvent } from "../web/errors/types";
import { handleErrorsMock } from "./errors-router";
import { createErrorsMockState } from "./errors-state";
import { json, mockError, numberField, readObject, stringField } from "./http";
import type { MockState } from "./state";
import { mockNow } from "./state";

const serviceSlug = (service: Service) =>
  service.name
    .trim()
    .toLowerCase()
    .replaceAll(/[^a-z0-9]+/gu, "-")
    .replaceAll(/^-|-$/gu, "") || service.id;

const createSecret = (prefix: string) =>
  `${prefix}_${crypto.randomUUID().replaceAll("-", "")}${crypto.randomUUID().replaceAll("-", "")}`;

const refreshOrigin = (state: MockState, serviceID: string) => {
  const configuration = state.serviceTelemetry[serviceID];
  const errors = state.serviceErrors[serviceID];
  if (!(configuration && errors)) {
    return;
  }
  configuration.internalDsn = `http://${serviceID}@${configuration.internalHostname}:9001/1`;
  configuration.publicDsn = configuration.publicHostname
    ? `https://${serviceID}@${configuration.publicHostname}/1`
    : undefined;
  errors.service.internalDsn = configuration.internalDsn;
  errors.service.publicDsn = configuration.publicDsn;
};

export const ensureServiceTelemetryMock = (
  state: MockState,
  service: Service
): ServiceTelemetry => {
  const existing = state.serviceTelemetry[service.id];
  if (existing) {
    return existing;
  }
  const canvas = state.canvases[service.projectId];
  const errors = createErrorsMockState();
  errors.service.id = service.id;
  errors.service.name = service.name;
  errors.service.slug = serviceSlug(service);
  for (const document of [
    ...errors.events,
    ...errors.artifacts,
    ...errors.replayItems,
    ...errors.replayRecording.errorEvents,
  ]) {
    document.service_id = service.id;
  }
  const internalHostname = `errors-${service.name}.${canvas?.project.name ?? "project"}.internal`;
  const internalOtlpEndpoint = `http://otel-${service.name}.${canvas?.project.name ?? "project"}.internal:4318`;
  const configuration: ServiceTelemetry = {
    internalDsn: "http://invalid.invalid/1",
    internalHostname,
    internalOtlpEndpoint,
    serviceId: service.id,
    trackedBy: (state.analyticsTrackers[service.projectId] ?? [])
      .filter((tracker) =>
        (state.domains[service.id] ?? []).some(
          (domain) =>
            domain.hostname === tracker.rootDomain ||
            domain.hostname.endsWith(`.${tracker.rootDomain}`)
        )
      )
      .map((tracker) => ({
        id: tracker.id,
        name: tracker.name,
        rootDomain: tracker.rootDomain,
      })),
    updatedAt: service.updatedAt,
    webhooks: structuredClone(errors.service.webhooks),
  };
  state.serviceErrors[service.id] = errors;
  state.metricCharts[service.id] = [];
  state.serviceTelemetry[service.id] = configuration;
  refreshOrigin(state, service.id);
  return configuration;
};

const metricChartFromInput = (
  input: Record<string, unknown>,
  id: string,
  createdAt: number
): ServiceMetricChart | undefined => {
  const visualization = stringField(input, "visualization");
  if (
    typeof input.title !== "string" ||
    typeof input.sql !== "string" ||
    !["area", "bar", "line", "value"].includes(visualization)
  ) {
    return;
  }
  return {
    createdAt,
    id,
    legend: stringField(input, "legend"),
    sql: input.sql,
    title: input.title,
    unit: stringField(input, "unit") || undefined,
    updatedAt: createdAt,
    visualization: visualization as ServiceMetricChart["visualization"],
  };
};

const handleMetricCharts = async (
  request: Request,
  state: MockState,
  scopeKey: string,
  tail: string[]
) => {
  const charts = (state.metricCharts[scopeKey] ??= []);
  if (tail.length === 0 && request.method === "GET") {
    return json(charts);
  }
  if (tail.length === 1 && request.method === "DELETE") {
    state.metricCharts[scopeKey] = charts.filter(
      (chart) => chart.id !== tail[0]
    );
    return new Response(null, { status: 204 });
  }
  if (tail.length === 1 && request.method === "PUT") {
    const current = charts.find((chart) => chart.id === tail[0]);
    if (!current) {
      return mockError("metric_chart_not_found", "Metric graph not found", 404);
    }
    const input = await readObject(request);
    if (numberField(input, "expectedUpdatedAt", -1) !== current.updatedAt) {
      return mockError(
        "service_telemetry_conflict",
        "Metric graph changed",
        409
      );
    }
    const updated = metricChartFromInput(input, current.id, current.createdAt);
    if (!updated) {
      return mockError(
        "invalid_metric_chart",
        "Metric chart fields are invalid",
        400
      );
    }
    Object.assign(current, updated, { updatedAt: mockNow() });
    return json(current);
  }
  if (tail.length !== 0 || request.method !== "POST") {
    return;
  }
  const input = await readObject(request);
  const timestamp = mockNow();
  const chart = metricChartFromInput(
    input,
    `chart_${crypto.randomUUID().replaceAll("-", "")}`,
    timestamp
  );
  if (!chart) {
    return mockError(
      "invalid_metric_chart",
      "Metric chart fields are invalid",
      400
    );
  }
  charts.push(chart);
  return json(chart, 201);
};

const handleWebhooks = async (
  request: Request,
  configuration: ServiceTelemetry,
  tail: string[]
) => {
  if (tail.length === 1 && request.method === "DELETE") {
    configuration.webhooks = configuration.webhooks.filter(
      (webhook) => webhook.id !== tail[0]
    );
    return new Response(null, { status: 204 });
  }
  if (tail.length !== 0 || request.method !== "POST") {
    return;
  }
  const input = await readObject(request);
  if (typeof input.url !== "string" || !Array.isArray(input.events)) {
    return mockError("invalid_webhook", "Webhook fields are invalid", 400);
  }
  const timestamp = mockNow();
  const webhook: CreatedWebhook = {
    createdAt: timestamp,
    enabled: true,
    events: input.events as WebhookEvent[],
    id: `webhook_${crypto.randomUUID().replaceAll("-", "")}`,
    secret: createSecret("ptel_whsec"),
    updatedAt: timestamp,
    url: input.url,
  };
  configuration.webhooks.push(webhook);
  return json(webhook, 201);
};

const updateBrowserTunnel = async (
  request: Request,
  configuration: ServiceTelemetry
) => {
  const input = await readObject(request);
  if (numberField(input, "expectedUpdatedAt", -1) !== configuration.updatedAt) {
    return mockError("service_telemetry_conflict", "Service changed", 409);
  }
  if (!configuration.publicHostname) {
    return mockError(
      "public_telemetry_required",
      "Browser tunnel requires a public telemetry domain",
      400
    );
  }
  if (typeof input.browserTunnelPath !== "string") {
    return mockError("invalid_browser_tunnel", "Tunnel path is invalid", 400);
  }
  configuration.browserTunnelPath = input.browserTunnelPath.trim() || undefined;
  configuration.updatedAt = mockNow();
  return json(configuration);
};

const updatePublicOTLPTraces = async (
  request: Request,
  configuration: ServiceTelemetry
) => {
  const input = await readObject(request);
  if (numberField(input, "expectedUpdatedAt", -1) !== configuration.updatedAt) {
    return mockError("service_telemetry_conflict", "Service changed", 409);
  }
  const publicHostname = stringField(input, "publicHostname").trim();
  const tracePath = stringField(input, "tracePath").trim();
  const invalidPath =
    tracePath.length > 256 ||
    tracePath === "/" ||
    (tracePath !== "" && !tracePath.startsWith("/")) ||
    tracePath.includes("?") ||
    tracePath.includes("#");
  if (Boolean(publicHostname) !== Boolean(tracePath) || invalidPath) {
    return mockError(
      "invalid_public_otlp_traces",
      "Public OTLP trace fields are invalid",
      400
    );
  }
  configuration.publicOtlpTraceHostname = publicHostname || undefined;
  configuration.publicOtlpTracePath = tracePath || undefined;
  configuration.publicOtlpTraceEndpoint = publicHostname
    ? `https://${publicHostname}${tracePath}`
    : undefined;
  configuration.updatedAt = mockNow();
  return json(configuration);
};

const handleTelemetryResource = async (
  request: Request,
  state: MockState,
  serviceID: string,
  configuration: ServiceTelemetry,
  action: string | undefined,
  tail: string[]
) => {
  if (!action && request.method === "GET") {
    return json(configuration);
  }
  if (
    action === "public-access" &&
    tail.length === 0 &&
    request.method === "PUT"
  ) {
    const input = await readObject(request);
    if (
      numberField(input, "expectedUpdatedAt", -1) !== configuration.updatedAt
    ) {
      return mockError("service_telemetry_conflict", "Service changed", 409);
    }
    configuration.publicHostname =
      stringField(input, "publicHostname") || undefined;
    if (!configuration.publicHostname) {
      configuration.browserTunnelPath = undefined;
    }
    configuration.updatedAt = mockNow();
    refreshOrigin(state, serviceID);
    return json(configuration);
  }
  if (
    action === "browser-tunnel" &&
    tail.length === 0 &&
    request.method === "PUT"
  ) {
    return updateBrowserTunnel(request, configuration);
  }
  if (
    action === "public-otlp-traces" &&
    tail.length === 0 &&
    request.method === "PUT"
  ) {
    return updatePublicOTLPTraces(request, configuration);
  }
  if (
    action === "artifact-token" &&
    tail.length === 0 &&
    request.method === "POST"
  ) {
    return json({ authToken: createSecret("ptel_artifact") });
  }
  if (action === "metric-charts") {
    return handleMetricCharts(request, state, serviceID, tail);
  }
  return action === "webhooks"
    ? handleWebhooks(request, configuration, tail)
    : undefined;
};

const mockTraceID = "4c79f60c11214eb38604f4ae0781bfb2";
const mockAITraceID = "6d79f60c11214eb38604f4ae0781bfa9";
const mockTraceSegmentID = "8f3a0f34b17c9d20";
const mockAITraceSegmentID = "8f3a0f34b17c9aa0";
const mockTraceStarted =
  BigInt(Date.parse("2026-08-09T10:45:03Z")) * 1_000_000n;
const nonAISummary = {
  aiAgent: "",
  aiAgentRunCount: 0,
  aiCacheReadTokens: null,
  aiCacheWriteTokens: null,
  aiCostUsd: null,
  aiInputTokens: null,
  aiModel: "",
  aiModelCallCount: 0,
  aiOutputTokens: null,
  aiProvider: "",
  aiReasoningTokens: null,
  aiTokensPerSecond: null,
  aiToolCallCount: 0,
  aiTtftSeconds: null,
  isAi: false,
} as const;
const nonAISpan = {
  aiAgent: "",
  aiCacheReadTokens: null,
  aiCacheWriteTokens: null,
  aiCostUsd: null,
  aiInputTokens: null,
  aiKind: "",
  aiModel: "",
  aiOperation: "",
  aiOutputTokens: null,
  aiProvider: "",
  aiReasoningTokens: null,
  aiTokensPerSecond: null,
  aiTtftSeconds: null,
  baselineDurationNano: null,
} as const;
const mockTraceSummaries = (): ServiceTraceSummary[] => [
  {
    ...nonAISummary,
    durationNano: "1380000000",
    errorSpanCount: 1,
    name: "POST /checkout/confirm",
    segmentId: mockTraceSegmentID,
    serviceId: "service-storefront",
    sources: ["otlp", "sentry"],
    spanCount: 3,
    startedAtUnixNano: mockTraceStarted.toString(),
    traceId: mockTraceID,
  },
  {
    aiAgent: "support-agent",
    aiAgentRunCount: 1,
    aiCacheReadTokens: 3180,
    aiCacheWriteTokens: 420,
    aiCostUsd: 0.0214,
    aiInputTokens: 4260,
    aiModel: "gpt-5-mini",
    aiModelCallCount: 2,
    aiOutputTokens: 690,
    aiProvider: "openai",
    aiReasoningTokens: 184,
    aiTokensPerSecond: 94.7,
    aiToolCallCount: 1,
    aiTtftSeconds: 0.38,
    durationNano: "4900000000",
    errorSpanCount: 0,
    isAi: true,
    name: "POST /support/reply",
    segmentId: mockAITraceSegmentID,
    serviceId: "service-storefront",
    sources: ["otlp"],
    spanCount: 6,
    startedAtUnixNano: (mockTraceStarted - 12_500_000_000n).toString(),
    traceId: mockAITraceID,
  },
];

const mockTraceDetail = (transactionPayload?: unknown): ServiceTraceDetail => ({
  metrics: [
    {
      name: "http.server.duration",
      spanId: "8f3a0f34b17c9d20",
      timeUnixNano: (mockTraceStarted + 1_380_000_000n).toString(),
      unit: "s",
      value: 1.38,
    },
  ],
  relatedSegments: [],
  segmentId: mockTraceSegmentID,
  spans: [
    {
      ...nonAISpan,
      durationNano: "1380000000",
      endTimeUnixNano: (mockTraceStarted + 1_380_000_000n).toString(),
      flags: 1,
      isSegment: true,
      kind: 2,
      name: "POST /checkout/confirm",
      parentSpanId: "",
      receivedAtUnixNano: (mockTraceStarted + 1_500_000_000n).toString(),
      resource: {
        attributes: [
          { key: "service.name", value: { stringValue: "storefront-api" } },
          {
            key: "deployment.environment",
            value: { stringValue: "production" },
          },
        ],
      },
      scope: { attributes: [], name: "sentry", version: "1" },
      segmentId: mockTraceSegmentID,
      serviceId: "service-storefront",
      source: "sentry",
      span: transactionPayload ?? {
        attributes: [
          { key: "http.request.method", value: { stringValue: "POST" } },
          { key: "http.response.status_code", value: { intValue: "500" } },
        ],
      },
      spanId: "8f3a0f34b17c9d20",
      startTimeUnixNano: mockTraceStarted.toString(),
      statusCode: 2,
      statusMessage: "inventory reservation expired",
      traceId: mockTraceID,
      traceState: "",
    },
    {
      ...nonAISpan,
      durationNano: "740000000",
      endTimeUnixNano: (mockTraceStarted + 910_000_000n).toString(),
      flags: 1,
      isSegment: false,
      kind: 3,
      name: "POST inventory/reserve",
      parentSpanId: "8f3a0f34b17c9d20",
      receivedAtUnixNano: (mockTraceStarted + 1_500_000_000n).toString(),
      resource: { attributes: [] },
      scope: { attributes: [], name: "@opentelemetry/instrumentation-fetch" },
      segmentId: mockTraceSegmentID,
      serviceId: "service-storefront",
      source: "otlp",
      span: {
        attributes: [
          {
            key: "server.address",
            value: { stringValue: "inventory.internal" },
          },
        ],
      },
      spanId: "c1e6fcf4377b32b1",
      startTimeUnixNano: (mockTraceStarted + 170_000_000n).toString(),
      statusCode: 0,
      statusMessage: "",
      traceId: mockTraceID,
      traceState: "",
    },
    {
      ...nonAISpan,
      durationNano: "310000000",
      endTimeUnixNano: (mockTraceStarted + 1_280_000_000n).toString(),
      flags: 1,
      isSegment: false,
      kind: 3,
      name: "SELECT cart_items",
      parentSpanId: "8f3a0f34b17c9d20",
      receivedAtUnixNano: (mockTraceStarted + 1_500_000_000n).toString(),
      resource: { attributes: [] },
      scope: { attributes: [], name: "@opentelemetry/instrumentation-pg" },
      segmentId: mockTraceSegmentID,
      serviceId: "service-storefront",
      source: "otlp",
      span: {
        attributes: [
          { key: "db.system", value: { stringValue: "postgresql" } },
        ],
      },
      spanId: "4c74bac87a902157",
      startTimeUnixNano: (mockTraceStarted + 970_000_000n).toString(),
      statusCode: 0,
      statusMessage: "",
      traceId: mockTraceID,
      traceState: "",
    },
  ],
  traceId: mockTraceID,
});

const mockAITraceDetail = (): ServiceTraceDetail => {
  const started = mockTraceStarted - 12_000_000_000n;
  const resource = {
    attributes: [
      { key: "service.name", value: { stringValue: "storefront-api" } },
      {
        key: "deployment.environment.name",
        value: { stringValue: "production" },
      },
    ],
  };
  const scope = { name: "@ai-sdk/otel", version: "1.0.58" };
  return {
    metrics: [],
    relatedSegments: [],
    segmentId: mockAITraceSegmentID,
    spans: [
      {
        ...nonAISpan,
        durationNano: "4900000000",
        endTimeUnixNano: (started + 4_400_000_000n).toString(),
        flags: 1,
        isSegment: true,
        kind: 2,
        name: "POST /support/reply",
        parentSpanId: "",
        receivedAtUnixNano: (started + 4_400_000_000n).toString(),
        resource,
        scope: {
          name: "@opentelemetry/instrumentation-http",
          version: "0.203.0",
        },
        segmentId: mockAITraceSegmentID,
        serviceId: "service-storefront",
        source: "otlp",
        span: {
          attributes: [
            { key: "http.request.method", value: { stringValue: "POST" } },
            {
              key: "url.path",
              value: { stringValue: "/support/reply" },
            },
          ],
        },
        spanId: "8f3a0f34b17c9aa0",
        startTimeUnixNano: (started - 500_000_000n).toString(),
        statusCode: 1,
        statusMessage: "",
        traceId: mockAITraceID,
        traceState: "",
      },
      {
        aiAgent: "support-agent",
        aiCacheReadTokens: 3180,
        aiCacheWriteTokens: 420,
        aiCostUsd: 0.0214,
        aiInputTokens: 4260,
        aiKind: "agent",
        aiModel: "gpt-5-mini",
        aiOperation: "invoke_agent",
        aiOutputTokens: 690,
        aiProvider: "openai",
        aiReasoningTokens: 184,
        aiTokensPerSecond: 94.7,
        aiTtftSeconds: 0.38,
        durationNano: "4280000000",
        endTimeUnixNano: (started + 4_280_000_000n).toString(),
        flags: 1,
        isSegment: false,
        kind: 1,
        name: "invoke_agent support-agent",
        parentSpanId: "8f3a0f34b17c9aa0",
        receivedAtUnixNano: (started + 4_400_000_000n).toString(),
        resource,
        scope,
        segmentId: mockAITraceSegmentID,
        serviceId: "service-storefront",
        source: "otlp",
        span: {
          attributes: [
            {
              key: "gen_ai.operation.name",
              value: { stringValue: "invoke_agent" },
            },
            {
              key: "gen_ai.agent.name",
              value: { stringValue: "support-agent" },
            },
            {
              key: "gen_ai.system_instructions",
              value: {
                stringValue: JSON.stringify([
                  {
                    content:
                      "You are the storefront support agent. Look up invoices before answering payment questions.",
                    type: "text",
                  },
                ]),
              },
            },
            {
              key: "gen_ai.request.temperature",
              value: { doubleValue: 0.2 },
            },
            {
              key: "gen_ai.request.max_tokens",
              value: { intValue: 800 },
            },
            {
              key: "ai.prompt.toolChoice",
              value: { stringValue: '{"type":"auto"}' },
            },
          ],
        },
        spanId: "8f3a0f34b17c9aa1",
        startTimeUnixNano: started.toString(),
        statusCode: 1,
        statusMessage: "",
        traceId: mockAITraceID,
        traceState: "",
      },
      {
        aiAgent: "support-agent",
        aiCacheReadTokens: null,
        aiCacheWriteTokens: null,
        aiCostUsd: null,
        aiInputTokens: null,
        aiKind: "step",
        aiModel: "",
        aiOperation: "agent_step",
        aiOutputTokens: null,
        aiProvider: "",
        aiReasoningTokens: null,
        aiTokensPerSecond: null,
        aiTtftSeconds: null,
        durationNano: "3860000000",
        endTimeUnixNano: (started + 4_050_000_000n).toString(),
        flags: 1,
        isSegment: false,
        kind: 1,
        name: "agent_step 1",
        parentSpanId: "8f3a0f34b17c9aa1",
        receivedAtUnixNano: (started + 4_400_000_000n).toString(),
        resource,
        scope,
        segmentId: mockAITraceSegmentID,
        serviceId: "service-storefront",
        source: "otlp",
        span: {
          attributes: [
            {
              key: "gen_ai.operation.name",
              value: { stringValue: "agent_step" },
            },
          ],
        },
        spanId: "8f3a0f34b17c9aa2",
        startTimeUnixNano: (started + 190_000_000n).toString(),
        statusCode: 1,
        statusMessage: "",
        traceId: mockAITraceID,
        traceState: "",
      },
      {
        aiAgent: "",
        aiCacheReadTokens: 2060,
        aiCacheWriteTokens: 420,
        aiCostUsd: null,
        aiInputTokens: 2740,
        aiKind: "model",
        aiModel: "gpt-5-mini",
        aiOperation: "chat",
        aiOutputTokens: 186,
        aiProvider: "openai",
        aiReasoningTokens: 72,
        aiTokensPerSecond: 103.3,
        aiTtftSeconds: 0.38,
        durationNano: "1800000000",
        endTimeUnixNano: (started + 2_080_000_000n).toString(),
        flags: 1,
        isSegment: false,
        kind: 3,
        name: "chat gpt-5-mini",
        parentSpanId: "8f3a0f34b17c9aa2",
        receivedAtUnixNano: (started + 4_400_000_000n).toString(),
        resource,
        scope,
        segmentId: mockAITraceSegmentID,
        serviceId: "service-storefront",
        source: "otlp",
        span: {
          attributes: [
            {
              key: "gen_ai.input.messages",
              value: {
                stringValue: JSON.stringify([
                  {
                    parts: [
                      {
                        content:
                          "Find invoice 42 and explain why the payment is pending.",
                        type: "text",
                      },
                    ],
                    role: "user",
                  },
                ]),
              },
            },
            {
              key: "gen_ai.system_instructions",
              value: {
                stringValue: JSON.stringify([
                  {
                    content:
                      "You are the storefront support agent. Look up invoices before answering payment questions.",
                    type: "text",
                  },
                ]),
              },
            },
            {
              key: "gen_ai.tool.definitions",
              value: {
                stringValue: JSON.stringify([
                  {
                    description: "Look up an invoice by ID",
                    name: "lookup_invoice",
                    parameters: {
                      properties: {
                        invoiceId: {
                          description: "Invoice identifier",
                          type: "string",
                        },
                      },
                      required: ["invoiceId"],
                      type: "object",
                    },
                    type: "function",
                  },
                ]),
              },
            },
            {
              key: "gen_ai.request.temperature",
              value: { doubleValue: 0.2 },
            },
            {
              key: "gen_ai.request.max_tokens",
              value: { intValue: 800 },
            },
            {
              key: "gen_ai.output.messages",
              value: {
                stringValue: JSON.stringify([
                  {
                    parts: [
                      {
                        arguments: { invoiceId: "42" },
                        id: "call_invoice_42",
                        name: "lookup_invoice",
                        type: "tool_call",
                      },
                    ],
                    role: "assistant",
                  },
                ]),
              },
            },
          ],
        },
        spanId: "8f3a0f34b17c9aa3",
        startTimeUnixNano: (started + 230_000_000n).toString(),
        statusCode: 1,
        statusMessage: "",
        traceId: mockAITraceID,
        traceState: "",
      },
      {
        aiAgent: "",
        aiCacheReadTokens: null,
        aiCacheWriteTokens: null,
        aiCostUsd: null,
        aiInputTokens: null,
        aiKind: "tool",
        aiModel: "",
        aiOperation: "execute_tool",
        aiOutputTokens: null,
        aiProvider: "",
        aiReasoningTokens: null,
        aiTokensPerSecond: null,
        aiTtftSeconds: null,
        durationNano: "410000000",
        endTimeUnixNano: (started + 2_610_000_000n).toString(),
        flags: 1,
        isSegment: false,
        kind: 1,
        name: "execute_tool lookup_invoice",
        parentSpanId: "8f3a0f34b17c9aa2",
        receivedAtUnixNano: (started + 4_400_000_000n).toString(),
        resource,
        scope,
        segmentId: mockAITraceSegmentID,
        serviceId: "service-storefront",
        source: "otlp",
        span: {
          attributes: [
            {
              key: "gen_ai.tool.name",
              value: { stringValue: "lookup_invoice" },
            },
            {
              key: "gen_ai.tool.call.arguments",
              value: { stringValue: '{"invoiceId":"42"}' },
            },
            {
              key: "gen_ai.tool.call.result",
              value: {
                stringValue:
                  '{"status":"pending","reason":"awaiting_bank_confirmation"}',
              },
            },
          ],
        },
        spanId: "8f3a0f34b17c9aa4",
        startTimeUnixNano: (started + 2_200_000_000n).toString(),
        statusCode: 1,
        statusMessage: "",
        traceId: mockAITraceID,
        traceState: "",
      },
      {
        aiAgent: "",
        aiCacheReadTokens: 1120,
        aiCacheWriteTokens: null,
        aiCostUsd: null,
        aiInputTokens: 1520,
        aiKind: "model",
        aiModel: "gpt-5-mini",
        aiOperation: "chat",
        aiOutputTokens: 504,
        aiProvider: "openai",
        aiReasoningTokens: 112,
        aiTokensPerSecond: 88.4,
        aiTtftSeconds: 0.31,
        durationNano: "1210000000",
        endTimeUnixNano: (started + 3_980_000_000n).toString(),
        flags: 1,
        isSegment: false,
        kind: 3,
        name: "chat gpt-5-mini",
        parentSpanId: "8f3a0f34b17c9aa2",
        receivedAtUnixNano: (started + 4_400_000_000n).toString(),
        resource,
        scope,
        segmentId: mockAITraceSegmentID,
        serviceId: "service-storefront",
        source: "otlp",
        span: {
          attributes: [
            {
              key: "gen_ai.output.messages",
              value: {
                stringValue: JSON.stringify([
                  {
                    parts: [
                      {
                        content:
                          "Invoice 42 is pending while the bank confirms the payment. No retry is needed yet.",
                        type: "text",
                      },
                    ],
                    role: "assistant",
                  },
                ]),
              },
            },
          ],
        },
        spanId: "8f3a0f34b17c9aa5",
        startTimeUnixNano: (started + 2_770_000_000n).toString(),
        statusCode: 1,
        statusMessage: "",
        traceId: mockAITraceID,
        traceState: "",
      },
    ],
    traceId: mockAITraceID,
  };
};

const mockTraceDurationMatches = (durationNano: string, expression: string) => {
  const match =
    /^(?<operator>>=|<=|>|<)?(?<amount>\d+(?:\.\d+)?)(?<unit>ms|s)$/u.exec(
      expression
    );
  if (!match?.groups) {
    return false;
  }
  const actual = Number(BigInt(durationNano)) / 1_000_000;
  const expected =
    Number(match.groups.amount) * (match.groups.unit === "s" ? 1000 : 1);
  if (match.groups.operator === ">") {
    return actual > expected;
  }
  if (match.groups.operator === ">=") {
    return actual >= expected;
  }
  if (match.groups.operator === "<") {
    return actual < expected;
  }
  if (match.groups.operator === "<=") {
    return actual <= expected;
  }
  return actual === expected;
};

const mockTraceList = (request: Request) => {
  const parameters = new URL(request.url).searchParams;
  const queryTokens = (parameters.get("query") ?? "")
    .toLocaleLowerCase()
    .split(/\s+/u)
    .filter(Boolean);
  const textTokens = queryTokens.filter(
    (token) => !(token.startsWith("status:") || token.startsWith("duration:"))
  );
  const statusFilters = [
    parameters.get("status") ?? "all",
    ...queryTokens
      .filter((token) => token.startsWith("status:"))
      .map((token) => token.slice("status:".length)),
  ];
  const durations = queryTokens
    .filter((token) => token.startsWith("duration:"))
    .map((token) => token.slice("duration:".length));
  const summaries = mockTraceSummaries().filter((summary) => {
    const searchable = [
      summary.name,
      summary.aiAgent,
      summary.aiModel,
      summary.aiProvider,
      summary.traceId,
      summary.isAi ? "invoice pending bank lookup tool" : "checkout",
    ]
      .join(" ")
      .toLocaleLowerCase();
    return (
      textTokens.every((token) => searchable.includes(token)) &&
      statusFilters.every(
        (status) =>
          status === "all" ||
          (status === "error"
            ? summary.errorSpanCount > 0
            : status === "ok" && summary.errorSpanCount === 0)
      ) &&
      durations.every((duration) =>
        mockTraceDurationMatches(summary.durationNano, duration)
      )
    );
  });
  const sort = parameters.get("sort") ?? "latest";
  return summaries.toSorted((left, right) => {
    if (sort === "slowest") {
      return Number(BigInt(right.durationNano) - BigInt(left.durationNano));
    }
    if (sort === "spans") {
      return right.spanCount - left.spanCount;
    }
    return Number(
      BigInt(right.startedAtUnixNano) - BigInt(left.startedAtUnixNano)
    );
  });
};

const handleTelemetryQuery = async (
  request: Request,
  action: string,
  tail: string[],
  transactionPayload?: unknown
) => {
  if (action === "traces") {
    if (request.method !== "GET") {
      return mockError("method_not_allowed", "Method not allowed", 405);
    }
    if (tail.length === 0) {
      return json(mockTraceList(request));
    }
    if (tail[0] === mockTraceID) {
      return json(mockTraceDetail(transactionPayload));
    }
    return tail[0] === mockAITraceID
      ? json(mockAITraceDetail())
      : mockError("not_found", "Trace not found", 404);
  }
  if (action === "metrics" && tail[0] === "catalog") {
    if (request.method !== "GET") {
      return mockError("method_not_allowed", "Method not allowed", 405);
    }
    return json([
      {
        attributeKeys: ["deployment.environment", "queue.name", "region"],
        description: "Pending checkout jobs",
        kind: "gauge",
        lastSeenUnixNano: (BigInt(Date.now()) * 1_000_000n).toString(),
        name: "checkout.queue.depth",
        unit: "{job}",
      },
      {
        attributeKeys: ["deployment.environment", "http.route", "region"],
        description: "Checkout processing latency",
        kind: "histogram",
        lastSeenUnixNano: (BigInt(Date.now()) * 1_000_000n).toString(),
        name: "checkout.duration",
        unit: "ms",
      },
    ]);
  }
  if (
    action === "metrics" &&
    tail[0] === "query" &&
    request.method === "POST"
  ) {
    const input = await readObject(request);
    const now = Date.now();
    const from = numberField(input, "from", now - 3_600_000);
    const to = numberField(input, "to", now);
    const step = Math.max(1000, numberField(input, "step", 20_000));
    const sql = stringField(input, "sql");
    if (!(sql.includes("metrics") && sql.includes("value"))) {
      return mockError("invalid_request", "metric SQL failed", 400);
    }
    const labels = /\bseries\b/iu.test(sql)
      ? ["eu-west", "us-east", "ap-south"]
      : [undefined];
    const result: {
      series?: string;
      timeUnixNano: string;
      value: number;
    }[] = [];
    for (let timestamp = from; timestamp <= to; timestamp += step) {
      for (const [labelIndex, series] of labels.entries()) {
        result.push({
          ...(series ? { series } : {}),
          timeUnixNano: (BigInt(timestamp) * 1_000_000n).toString(),
          value:
            18 +
            Math.sin(timestamp / 210_000 + labelIndex) * (8 - labelIndex) +
            ((timestamp / step) % 5) +
            labelIndex * 5,
        });
      }
    }
    return json(result);
  }
};

export const handleMetricScopeTelemetry = (
  request: Request,
  state: MockState,
  scopeKey: string,
  rest: string[]
): Promise<Response | undefined> | Response | undefined => {
  const [action, ...tail] = rest;
  if (action === "metrics" || action === "traces") {
    return handleTelemetryQuery(request, action, tail);
  }
  const serviceIDs = Object.values(state.services)
    .filter(
      (service) =>
        scopeKey === "installation" ||
        service.projectId === scopeKey.slice("project:".length)
    )
    .map((service) => service.id);
  for (const serviceID of serviceIDs) {
    const service = state.services[serviceID];
    if (service) {
      ensureServiceTelemetryMock(state, service);
    }
  }
  if (action === "logs" && request.method === "GET") {
    const parameters = new URL(request.url).searchParams;
    const contains = (parameters.get("contains") ?? "").toLocaleLowerCase();
    const traceID = parameters.get("traceId") ?? "";
    const spanID = parameters.get("spanId") ?? "";
    const records = serviceIDs
      .flatMap((serviceID) =>
        (state.logs[serviceID]?.records ?? []).map((record) => ({
          ...record,
          serviceId: serviceID,
        }))
      )
      .filter(
        (record) =>
          (!contains ||
            JSON.stringify(record).toLocaleLowerCase().includes(contains)) &&
          (!traceID || record.traceId === traceID) &&
          (!spanID || record.spanId === spanID)
      )
      .toSorted(
        (left, right) =>
          Date.parse(right.timestamp) - Date.parse(left.timestamp)
      );
    return json({ records, truncated: false });
  }
  if (action === "errors" && tail[0] === "issues" && request.method === "GET") {
    const query = (
      new URL(request.url).searchParams.get("query") ?? ""
    ).toLocaleLowerCase();
    const data = serviceIDs.flatMap((serviceID) =>
      (state.serviceErrors[serviceID]?.issues ?? [])
        .filter(
          (issue) =>
            !query || JSON.stringify(issue).toLocaleLowerCase().includes(query)
        )
        .map((issue) => ({
          ...issue,
          projectId: state.services[serviceID]?.projectId,
          serviceId: serviceID,
        }))
    );
    return json({ data, total: data.length });
  }
  return action === "metric-charts"
    ? handleMetricCharts(request, state, scopeKey, tail)
    : undefined;
};

const handleErrorsResource = (
  request: Request,
  state: MockState,
  serviceID: string,
  action: string,
  tail: string[]
) => {
  const errors = state.serviceErrors[serviceID];
  if (!errors) {
    return mockError(
      "service_telemetry_unavailable",
      "Service telemetry unavailable",
      503
    );
  }
  const url = new URL(request.url);
  url.pathname = `/${[action, ...tail].join("/")}`;
  return handleErrorsMock(new Request(url, request), errors);
};

export const handleServiceTelemetry = (
  request: Request,
  state: MockState,
  projectID: string,
  serviceID: string,
  rest: string[]
): Promise<Response | undefined> | Response | undefined => {
  const service = state.services[serviceID];
  if (!service || service.projectId !== projectID) {
    return rest[0] === "telemetry" || rest[0] === "errors"
      ? mockError("service_not_found", "Service not found", 404)
      : undefined;
  }
  const configuration = ensureServiceTelemetryMock(state, service);
  const [resource, action, ...tail] = rest;
  if (resource === "telemetry") {
    if (action === "traces" || action === "metrics") {
      return handleTelemetryQuery(
        request,
        action,
        tail,
        state.serviceErrors[serviceID]?.events[0]?.payload
      );
    }
    return handleTelemetryResource(
      request,
      state,
      serviceID,
      configuration,
      action,
      tail
    );
  }
  if (resource === "errors" && action) {
    return handleErrorsResource(request, state, serviceID, action, tail);
  }
  return undefined;
};
