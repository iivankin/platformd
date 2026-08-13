import type { ServiceTraceSpan } from "@/api";
import { asRecord } from "@/errors/event-context";

export interface AiMessagePart {
  content?: string;
  input?: unknown;
  name?: string;
  output?: unknown;
  toolCallID?: string;
  type: "reasoning" | "text" | "tool-call" | "tool-result" | "unknown";
}

export interface AiMessage {
  id: string;
  parts: AiMessagePart[];
  role: string;
}

export interface AiRun {
  agent: boolean;
  root: ServiceTraceSpan;
  spans: ServiceTraceSpan[];
}

export const otlpNativeValue = (value: unknown): unknown => {
  if (value === null || value === undefined || typeof value !== "object") {
    return value;
  }
  const record = value as Record<string, unknown>;
  for (const candidate of [
    "stringValue",
    "intValue",
    "doubleValue",
    "boolValue",
  ]) {
    if (record[candidate] !== undefined) {
      return record[candidate];
    }
  }
  const array = asRecord(record.arrayValue)?.values;
  if (Array.isArray(array)) {
    return array.map(otlpNativeValue);
  }
  const pairs = asRecord(record.kvlistValue)?.values;
  if (Array.isArray(pairs)) {
    return Object.fromEntries(
      pairs.flatMap((pair) => {
        const entry = asRecord(pair);
        return entry && typeof entry.key === "string"
          ? [[entry.key, otlpNativeValue(entry.value)] as const]
          : [];
      })
    );
  }
  if (record.bytesValue !== undefined) {
    return "[binary]";
  }
  return value;
};

export const otlpAttributes = (value: unknown) => {
  const attributes = asRecord(value)?.attributes;
  if (!Array.isArray(attributes)) {
    return [];
  }
  return attributes.flatMap((item) => {
    const record = asRecord(item);
    return record && typeof record.key === "string"
      ? [{ key: record.key, value: otlpNativeValue(record.value) }]
      : [];
  });
};

export const otlpAttributeMap = (value: unknown) =>
  new Map(
    otlpAttributes(value).map((attribute) => [attribute.key, attribute.value])
  );

export const otlpAttribute = (value: unknown, key: string) =>
  otlpAttributes(value).find((item) => item.key === key)?.value;

const parsed = (value: unknown) => {
  if (typeof value !== "string") {
    return value;
  }
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return value;
  }
};

const firstValue = (values: Map<string, unknown>, keys: string[]) => {
  for (const key of keys) {
    if (values.has(key)) {
      return parsed(values.get(key));
    }
  }
};

const stringValue = (record: Record<string, unknown>, keys: string[]) => {
  for (const key of keys) {
    if (typeof record[key] === "string" && record[key]) {
      return record[key] as string;
    }
  }
};

const normalizePart = (value: unknown): AiMessagePart => {
  if (typeof value === "string") {
    return { content: value, type: "text" };
  }
  const part = asRecord(value);
  if (!part) {
    return { content: JSON.stringify(value), type: "unknown" };
  }
  const rawType = stringValue(part, ["type", "part_kind", "kind"]) ?? "";
  const normalizedType = rawType.replaceAll("_", "-").toLowerCase();
  const content = stringValue(part, ["content", "text", "reasoning"]);
  const name = stringValue(part, ["name", "toolName", "tool_name"]);
  const toolCallID = stringValue(part, [
    "id",
    "toolCallId",
    "tool_call_id",
    "call_id",
  ]);
  if (normalizedType.includes("reasoning") || normalizedType === "thinking") {
    return { content, type: "reasoning" };
  }
  if (
    normalizedType.includes("tool-call") ||
    normalizedType === "tool-use" ||
    normalizedType === "tool"
  ) {
    return {
      input: parsed(part.input ?? part.args ?? part.arguments),
      name,
      toolCallID,
      type: "tool-call",
    };
  }
  if (
    normalizedType.includes("tool-result") ||
    normalizedType.includes("tool-return") ||
    normalizedType.includes("tool-response")
  ) {
    return {
      name,
      output: parsed(part.output ?? part.result ?? part.content),
      toolCallID,
      type: "tool-result",
    };
  }
  if (content !== undefined || normalizedType.includes("text")) {
    return { content: content ?? "", type: "text" };
  }
  return { content: JSON.stringify(part), type: "unknown" };
};

const messageRole = (message: Record<string, unknown>, parts: unknown[]) => {
  const role = stringValue(message, ["role"]);
  if (role) {
    return role;
  }
  const kind = stringValue(message, ["kind"]);
  if (kind === "request") {
    const part = asRecord(parts[0]);
    const partKind = part ? stringValue(part, ["part_kind", "type"]) : "";
    return partKind?.includes("system") ? "system" : "user";
  }
  if (kind === "response") {
    return "assistant";
  }
  return "message";
};

const normalizeMessages = (value: unknown, source: string): AiMessage[] => {
  const input = parsed(value);
  let messages: unknown[];
  if (Array.isArray(input)) {
    messages = input;
  } else if (input === undefined) {
    messages = [];
  } else {
    messages = [input];
  }
  return messages.flatMap((message, index) => {
    if (typeof message === "string") {
      return [
        {
          id: `${source}:${index.toString()}`,
          parts: [{ content: message, type: "text" as const }],
          role: source,
        },
      ];
    }
    const record = asRecord(message);
    if (!record) {
      return [];
    }
    const rawParts = record.parts ?? record.content ?? record.contents;
    const parts = Array.isArray(rawParts) ? rawParts : [rawParts ?? record];
    return [
      {
        id:
          stringValue(record, ["id", "message_id"]) ??
          `${source}:${index.toString()}`,
        parts: parts.map(normalizePart),
        role: messageRole(record, parts),
      },
    ];
  });
};

export const aiMessages = (span: ServiceTraceSpan): AiMessage[] => {
  const attributes = otlpAttributeMap(span.span);
  const allMessages = firstValue(attributes, ["pydantic_ai.all_messages"]);
  if (allMessages !== undefined) {
    return normalizeMessages(allMessages, "message");
  }
  const input = firstValue(attributes, [
    "gen_ai.input.messages",
    "ai.prompt.messages",
    "ai.prompt",
  ]);
  const output = firstValue(attributes, [
    "gen_ai.output.messages",
    "ai.response.messages",
    "ai.response.text",
  ]);
  return [
    ...normalizeMessages(input, "user"),
    ...normalizeMessages(output, "assistant"),
  ];
};

export const aiTool = (span: ServiceTraceSpan) => {
  const attributes = otlpAttributeMap(span.span);
  return {
    input: firstValue(attributes, [
      "gen_ai.tool.call.arguments",
      "ai.toolCall.args",
    ]),
    name:
      firstValue(attributes, ["gen_ai.tool.name", "ai.toolCall.name"]) ??
      span.name,
    output: firstValue(attributes, [
      "gen_ai.tool.call.result",
      "ai.toolCall.result",
    ]),
    toolCallID: firstValue(attributes, [
      "gen_ai.tool.call.id",
      "ai.toolCall.id",
    ]),
  };
};

export const aiRuns = (spans: ServiceTraceSpan[]): AiRun[] => {
  const children = new Map<string, string[]>();
  for (const span of spans) {
    if (!span.parentSpanId) {
      continue;
    }
    const siblings = children.get(span.parentSpanId);
    if (siblings) {
      siblings.push(span.spanId);
    } else {
      children.set(span.parentSpanId, [span.spanId]);
    }
  }
  const subtree = (root: ServiceTraceSpan) => {
    const included = new Set<string>();
    const pending = [root.spanId];
    while (pending.length > 0) {
      const spanID = pending.pop();
      if (!spanID || included.has(spanID)) {
        continue;
      }
      included.add(spanID);
      pending.push(...(children.get(spanID) ?? []));
    }
    return spans.filter((span) => included.has(span.spanId));
  };
  const agents = spans.filter((span) => span.aiKind === "agent");
  if (agents.length > 0) {
    return agents.map((root) => ({ agent: true, root, spans: subtree(root) }));
  }
  const root = spans.find((span) => Boolean(span.aiKind));
  return root
    ? [
        {
          agent: false,
          root,
          spans: spans.filter((span) => Boolean(span.aiKind)),
        },
      ]
    : [];
};

export const aiSpanLabel = (span: ServiceTraceSpan) => {
  if (span.aiKind === "tool") {
    return String(aiTool(span).name);
  }
  if (span.aiKind === "agent" || span.aiKind === "step") {
    return span.aiAgent || span.name;
  }
  return span.aiModel || span.aiAgent || span.name;
};
