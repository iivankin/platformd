import { describe, expect, test } from "bun:test";

import { calculateAiPrice } from "@/ai-price";
import { aiMessages, aiRuns, aiTool } from "@/ai-trace";
import type { ServiceTraceSpan } from "@/api";

const span = (attributes: unknown[]): ServiceTraceSpan => ({
  aiAgent: "",
  aiCacheReadTokens: 800,
  aiCacheWriteTokens: 0,
  aiCostUsd: null,
  aiInputTokens: 1000,
  aiKind: "model",
  aiModel: "gpt-5-mini",
  aiOperation: "chat",
  aiOutputTokens: 100,
  aiProvider: "openai",
  aiReasoningTokens: 20,
  aiTokensPerSecond: 50,
  aiTtftSeconds: 0.3,
  durationNano: "2000000000",
  endTimeUnixNano: "3000000000",
  flags: 1,
  isSegment: true,
  kind: 3,
  name: "chat gpt-5-mini",
  parentSpanId: "",
  receivedAtUnixNano: "4000000000",
  resource: {},
  scope: {},
  segmentId: "0123456789abcdef",
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

  test("prefers reported cost and otherwise estimates cache-aware model cost", () => {
    const reported = calculateAiPrice({
      actual: 0.0042,
      model: "unknown",
      provider: "unknown",
      usage: {},
    });
    expect(reported).toMatchObject({ estimated: false, value: 0.0042 });

    const estimated = calculateAiPrice({
      model: "gpt-5-mini",
      provider: "openai",
      timestamp: new Date("2026-08-09T00:00:00Z"),
      usage: {
        cacheReadTokens: 800,
        inputTokens: 1000,
        outputTokens: 100,
      },
    });
    expect(estimated?.estimated).toBe(true);
    expect(estimated?.value).toBeGreaterThan(0);
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
