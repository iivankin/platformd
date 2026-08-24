import { describe, expect, test } from "bun:test";

import { storedAiPrice } from "@/ai-price";
import {
  aiMessages,
  aiRequestSettings,
  aiRuns,
  aiTool,
  aiToolDefinitions,
  isSensitiveAiAttributeKey,
  redactSensitiveAiValue,
} from "@/ai-trace";
import type { ServiceTraceSpan } from "@/api";

const span = (attributes: unknown[]): ServiceTraceSpan => ({
  aiAgent: "",
  aiCacheReadTokens: 800,
  aiCacheWriteTokens: 0,
  aiCostUsd: null,
  aiEstimatedCostUsd: 0.001,
  aiInputTokens: 1000,
  aiKind: "model",
  aiModel: "gpt-5-mini",
  aiOperation: "chat",
  aiOutputTokens: 100,
  aiProvider: "openai",
  aiReasoningTokens: 20,
  aiSessionId: "",
  aiTokensPerSecond: 50,
  aiTtftSeconds: 0.3,
  aiUserId: "",
  durationNano: "2000000000",
  endTimeUnixNano: "3000000000",
  flags: 1,
  kind: 3,
  name: "chat gpt-5-mini",
  parentSpanId: "",
  receivedAtUnixNano: "4000000000",
  replayId: "",
  resource: {},
  scope: {},
  serviceId: "service-test",
  source: "otlp",
  span: { attributes },
  spanId: "0123456789abcdef",
  startTimeUnixNano: "1000000000",
  statusCode: 1,
  statusMessage: "",
  traceId: "0123456789abcdef0123456789abcdef",
  traceState: "",
});

const attribute = (key: string, value: string) => ({
  key,
  value: { stringValue: value },
});

describe("AI trace normalization", () => {
  test("recognizes credential-like AI context keys", () => {
    expect(isSensitiveAiAttributeKey("ai.settings.context.secret")).toBe(true);
    expect(isSensitiveAiAttributeKey("ai.settings.context.password")).toBe(
      true
    );
    expect(isSensitiveAiAttributeKey("ai.settings.runtimeContext.apiKey")).toBe(
      true
    );
    expect(isSensitiveAiAttributeKey("ai.settings.context.authorization")).toBe(
      true
    );
    expect(
      isSensitiveAiAttributeKey("ai.settings.runtimeContext.accessToken")
    ).toBe(true);
    for (const key of [
      "authToken",
      "bearerToken",
      "refreshToken",
      "sessionToken",
    ]) {
      expect(
        isSensitiveAiAttributeKey(`ai.settings.runtimeContext.${key}`)
      ).toBe(true);
    }
    expect(
      isSensitiveAiAttributeKey("ai.settings.runtimeContext.private_key")
    ).toBe(true);
    expect(isSensitiveAiAttributeKey("ai.settings.runtimeContext.token")).toBe(
      true
    );
    expect(
      isSensitiveAiAttributeKey("ai.settings.runtimeContext.idToken")
    ).toBe(true);
    expect(isSensitiveAiAttributeKey("ai.settings.context.jwt")).toBe(true);
    expect(isSensitiveAiAttributeKey("ai.settings.context.passphrase")).toBe(
      true
    );
    expect(isSensitiveAiAttributeKey("ai.settings.context.userId")).toBe(false);
  });

  test("redacts nested credentials from AI context values", () => {
    expect(
      redactSensitiveAiValue(
        JSON.stringify({
          auth: { jwt: "hidden-jwt", region: "eu" },
          idToken: "hidden-token",
          organization: "acme",
          profiles: [{ passphrase: "hidden-passphrase", role: "admin" }],
        })
      )
    ).toEqual({
      auth: { region: "eu" },
      organization: "acme",
      profiles: [{ role: "admin" }],
    });
  });

  test("renders standard messages and tool calls as semantic content", () => {
    const value = span([
      attribute(
        "gen_ai.input.messages",
        JSON.stringify([
          {
            parts: [{ content: "Find invoice 42", type: "text" }],
            role: "user",
          },
        ])
      ),
      attribute(
        "gen_ai.output.messages",
        JSON.stringify([
          {
            parts: [
              {
                arguments: { invoiceId: "42" },
                id: "call-1",
                name: "lookup_invoice",
                type: "tool_call",
              },
            ],
            role: "assistant",
          },
        ])
      ),
    ]);

    const messages = aiMessages(value);
    expect(messages).toHaveLength(2);
    expect(messages[0]?.parts[0]).toMatchObject({
      content: "Find invoice 42",
      type: "text",
    });
    expect(messages[1]?.parts[0]).toMatchObject({
      input: { invoiceId: "42" },
      name: "lookup_invoice",
      toolCallID: "call-1",
      type: "tool-call",
    });
  });

  test("surfaces system instructions, available tools, and request settings", () => {
    const value = span([
      attribute(
        "gen_ai.system_instructions",
        JSON.stringify([
          { content: "You are the invoice assistant.", type: "text" },
        ])
      ),
      attribute(
        "gen_ai.input.messages",
        JSON.stringify([
          {
            parts: [{ content: "Find invoice 42", type: "text" }],
            role: "user",
          },
        ])
      ),
      attribute(
        "gen_ai.output.messages",
        JSON.stringify([
          {
            parts: [
              {
                id: "call-1",
                name: "lookup_invoice",
                response: { status: "pending" },
                type: "tool_call_response",
              },
            ],
            role: "tool",
          },
        ])
      ),
      attribute(
        "gen_ai.tool.definitions",
        JSON.stringify([
          {
            description: "Look up an invoice by ID",
            name: "lookup_invoice",
            parameters: {
              properties: { invoiceId: { type: "string" } },
              required: ["invoiceId"],
              type: "object",
            },
            type: "function",
          },
        ])
      ),
      attribute("gen_ai.request.temperature", "0"),
      attribute("gen_ai.request.max_tokens", "800"),
      attribute("ai.prompt.toolChoice", '{"type":"auto"}'),
    ]);

    const messages = aiMessages(value);
    expect(messages[0]).toMatchObject({
      parts: [{ content: "You are the invoice assistant.", type: "text" }],
      role: "system",
    });
    expect(messages.at(-1)?.parts[0]).toMatchObject({
      output: { status: "pending" },
      toolCallID: "call-1",
      type: "tool-result",
    });
    expect(aiToolDefinitions(value)).toEqual([
      {
        description: "Look up an invoice by ID",
        name: "lookup_invoice",
        parameters: {
          properties: { invoiceId: { type: "string" } },
          required: ["invoiceId"],
          type: "object",
        },
        type: "function",
      },
    ]);
    expect(aiRequestSettings(value)).toEqual(
      expect.arrayContaining([
        { label: "Temperature", value: 0 },
        { label: "Max tokens", value: 800 },
        { label: "Tool choice", value: { type: "auto" } },
      ])
    );
  });

  test("reads AI SDK tool definitions and nested function call arguments", () => {
    const value = span([
      attribute(
        "ai.prompt.tools",
        JSON.stringify([
          JSON.stringify({
            description: "Look up an invoice by ID",
            inputSchema: {
              properties: { invoiceId: { type: "string" } },
              type: "object",
            },
            name: "lookup_invoice",
            type: "function",
          }),
        ])
      ),
      attribute(
        "ai.response.toolCalls",
        JSON.stringify([
          {
            function: {
              arguments: '{"invoiceId":"42"}',
              name: "lookup_invoice",
            },
            id: "call-1",
            type: "function",
          },
        ])
      ),
    ]);

    expect(aiToolDefinitions(value)[0]).toMatchObject({
      name: "lookup_invoice",
      parameters: {
        properties: { invoiceId: { type: "string" } },
        type: "object",
      },
    });
    expect(aiMessages(value).at(-1)?.parts[0]).toMatchObject({
      input: { invoiceId: "42" },
      name: "lookup_invoice",
      toolCallID: "call-1",
      type: "tool-call",
    });
  });

  test("decodes direct tool arguments and results", () => {
    const value = {
      ...span([
        attribute("gen_ai.tool.name", "lookup_invoice"),
        attribute("gen_ai.tool.call.arguments", '{"invoiceId":"42"}'),
        attribute("gen_ai.tool.call.result", '{"status":"pending"}'),
      ]),
      aiKind: "tool",
    };

    expect(aiTool(value)).toMatchObject({
      input: { invoiceId: "42" },
      name: "lookup_invoice",
      output: { status: "pending" },
    });
  });

  test("skips empty standard attributes when reading AI SDK fallbacks", () => {
    const value = {
      ...span([
        attribute("gen_ai.tool.name", ""),
        attribute("ai.toolCall.name", "lookup_invoice"),
        attribute("gen_ai.tool.call.arguments", " "),
        attribute("ai.toolCall.args", '{"invoiceId":"42"}'),
        attribute("gen_ai.tool.call.result", "null"),
        attribute("ai.toolCall.result", '{"status":"pending"}'),
        attribute("gen_ai.request.max_tokens", ""),
        attribute("ai.settings.maxOutputTokens", "800"),
      ]),
      aiKind: "tool",
    };

    expect(aiTool(value)).toMatchObject({
      input: { invoiceId: "42" },
      name: "lookup_invoice",
      output: { status: "pending" },
    });
    expect(aiRequestSettings(value)).toContainEqual({
      label: "Max tokens",
      value: 800,
    });
  });

  test("combines stored reported and estimated cost with provenance", () => {
    const reported = storedAiPrice({
      actual: 0.0042,
      estimated: null,
      model: "unknown",
      provider: "unknown",
    });
    expect(reported).toMatchObject({ estimated: false, value: 0.0042 });

    const estimated = storedAiPrice({
      actual: 0.0042,
      estimated: 0.001,
      model: "gpt-5-mini",
      provider: "openai",
    });
    expect(estimated?.estimated).toBe(true);
    expect(estimated?.value).toBe(0.0052);
  });

  test("scopes nested agent runs without absorbing the enclosing trace", () => {
    const linked = (
      spanId: string,
      parentSpanId: string,
      aiKind: string,
      name: string
    ): ServiceTraceSpan => ({
      ...span([]),
      aiAgent: aiKind === "agent" ? name : "",
      aiKind,
      name,
      parentSpanId,
      spanId,
    });
    const spans = [
      linked("http", "", "", "POST /checkout"),
      linked("agent-a", "http", "agent", "fraud-agent"),
      linked("model-a", "agent-a", "model", "chat gpt-5-mini"),
      linked("db-a", "agent-a", "", "SELECT risk_score"),
      linked("agent-b", "http", "agent", "receipt-agent"),
      linked("model-b", "agent-b", "model", "chat gpt-5-mini"),
    ];

    const runs = aiRuns(spans);
    expect(runs).toHaveLength(2);
    expect(runs[0]?.root.spanId).toBe("agent-a");
    expect(runs[0]?.spans.map((item) => item.spanId)).toEqual([
      "agent-a",
      "model-a",
      "db-a",
    ]);
    expect(runs[1]?.spans.map((item) => item.spanId)).toEqual([
      "agent-b",
      "model-b",
    ]);
  });
});
