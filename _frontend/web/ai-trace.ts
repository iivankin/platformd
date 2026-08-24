import type { ServiceTraceSpan } from "@/api";
import { asRecord } from "@/errors/event-context";
import { otlpAttributeMap } from "@/otlp";

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

export interface AiToolDefinition {
  description?: string;
  name: string;
  parameters?: unknown;
  type: string;
}

export interface AiSettingRow {
  label: string;
  value: unknown;
}

export interface AiRun {
  agent: boolean;
  root: ServiceTraceSpan;
  spans: ServiceTraceSpan[];
}

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

const hasFallbackValue = (value: unknown) =>
  value !== undefined &&
  value !== null &&
  (typeof value !== "string" || value.trim() !== "");

const firstValue = (values: Map<string, unknown>, keys: string[]) => {
  for (const key of keys) {
    if (values.has(key)) {
      const value = parsed(values.get(key));
      if (hasFallbackValue(value)) {
        return value;
      }
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

const asList = (value: unknown): unknown[] => {
  const input = parsed(value);
  if (input === undefined || input === null) {
    return [];
  }
  return Array.isArray(input) ? input : [input];
};

export const isSensitiveAiAttributeKey = (key: string) =>
  /secret|password|passphrase|authorization|token|jwt|api[_-]?key|private[_-]?key|credential|cookie/iu.test(
    key
  );

export const redactSensitiveAiValue = (value: unknown): unknown => {
  const parsedValue = parsed(value);
  const input =
    typeof value === "string" &&
    (parsedValue === null || typeof parsedValue !== "object")
      ? value
      : parsedValue;
  if (Array.isArray(input)) {
    return input.map(redactSensitiveAiValue);
  }
  const record = asRecord(input);
  if (!record) {
    return input;
  }
  return Object.fromEntries(
    Object.entries(record).flatMap(([key, entry]) =>
      isSensitiveAiAttributeKey(key)
        ? []
        : [[key, redactSensitiveAiValue(entry)]]
    )
  );
};

const partType = (part: Record<string, unknown>) =>
  (stringValue(part, ["type", "part_kind", "kind"]) ?? "")
    .replaceAll("_", "-")
    .toLowerCase();

const toolName = (
  part: Record<string, unknown>,
  fn?: Record<string, unknown>
) =>
  stringValue(fn ?? {}, ["name"]) ??
  stringValue(part, ["name", "toolName", "tool_name"]);

const partToolCallID = (part: Record<string, unknown>) =>
  stringValue(part, ["id", "toolCallId", "tool_call_id", "call_id"]);

const isToolResultType = (type: string) =>
  type.includes("tool-result") ||
  type.includes("tool-return") ||
  type.includes("tool-response") ||
  type.includes("tool-call-response");

const isToolCallType = (type: string) =>
  type.includes("tool-call") ||
  type === "tool-use" ||
  type === "tool" ||
  type === "function" ||
  type === "function-call";

const normalizeRecordPart = (part: Record<string, unknown>): AiMessagePart => {
  const fn = asRecord(part.function);
  const type = partType(part);
  const content = stringValue(part, ["content", "text", "reasoning"]);
  const name = toolName(part, fn);
  const id = partToolCallID(part);
  if (type.includes("reasoning") || type === "thinking") {
    return { content, type: "reasoning" };
  }
  if (isToolResultType(type)) {
    return {
      name,
      output: parsed(
        part.output ?? part.result ?? part.response ?? part.content
      ),
      toolCallID: id,
      type: "tool-result",
    };
  }
  if (isToolCallType(type) || fn) {
    return {
      input: parsed(
        part.input ?? part.args ?? part.arguments ?? fn?.arguments ?? fn?.input
      ),
      name,
      toolCallID: id,
      type: "tool-call",
    };
  }
  if (content !== undefined || type.includes("text")) {
    return { content: content ?? "", type: "text" };
  }
  return { content: JSON.stringify(part), type: "unknown" };
};

const normalizePart = (value: unknown): AiMessagePart => {
  if (typeof value === "string") {
    return { content: value, type: "text" };
  }
  const part = asRecord(value);
  if (!part) {
    return { content: JSON.stringify(value), type: "unknown" };
  }
  return normalizeRecordPart(part);
};

const messageParts = (record: Record<string, unknown>): unknown[] => {
  const parts: unknown[] = [];
  if (Array.isArray(record.parts)) {
    parts.push(...record.parts);
  } else if (Array.isArray(record.contents)) {
    parts.push(...record.contents);
  } else if (Array.isArray(record.content)) {
    parts.push(...record.content);
  } else if (record.content !== undefined && record.content !== null) {
    parts.push(record.content);
  }
  if (Array.isArray(record.tool_calls)) {
    parts.push(...record.tool_calls);
  }
  return parts.length > 0 ? parts : [record];
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
  } else if (input === undefined || input === null) {
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
    const rawParts = messageParts(record);
    const role = messageRole(record, rawParts);
    const toolCallID = stringValue(record, [
      "tool_call_id",
      "toolCallId",
      "id",
    ]);
    const parts = rawParts.map((part) => {
      const normalized = normalizePart(part);
      if (role === "tool" && normalized.type === "text") {
        return {
          name: stringValue(record, ["name", "toolName", "tool_name"]),
          output: normalized.content,
          toolCallID,
          type: "tool-result" as const,
        };
      }
      return normalized;
    });
    return [
      {
        id:
          stringValue(record, ["id", "message_id"]) ??
          `${source}:${index.toString()}`,
        parts,
        role,
      },
    ];
  });
};

const promptBag = (value: unknown) => {
  const record = asRecord(parsed(value));
  if (
    !record ||
    Array.isArray(value) ||
    (record.messages === undefined &&
      record.prompt === undefined &&
      record.system === undefined)
  ) {
    return { messages: value, system: undefined as unknown };
  }
  return {
    messages: record.messages ?? record.prompt,
    system: record.system,
  };
};

const systemParts = (value: unknown): AiMessagePart[] => {
  const input = parsed(value);
  if (input === undefined || input === null || input === "") {
    return [];
  }
  if (typeof input === "string") {
    return [{ content: input, type: "text" }];
  }
  if (Array.isArray(input) && input.some((item) => asRecord(item)?.role)) {
    return normalizeMessages(input, "system").flatMap(
      (message) => message.parts
    );
  }
  if (Array.isArray(input)) {
    return input.map(normalizePart);
  }
  const record = asRecord(input);
  if (record && (record.role || record.parts || record.content)) {
    return normalizeMessages(input, "system").flatMap(
      (message) => message.parts
    );
  }
  return [normalizePart(input)];
};

const withSystem = (messages: AiMessage[], value: unknown): AiMessage[] => {
  const parts = systemParts(value);
  if (
    parts.length === 0 ||
    messages.some((message) => message.role === "system")
  ) {
    return messages;
  }
  return [{ id: "system", parts, role: "system" }, ...messages];
};

const withResponseToolCalls = (
  messages: AiMessage[],
  value: unknown
): AiMessage[] => {
  const parts = asList(value).flatMap((item) => {
    const record = asRecord(parsed(item));
    const part = normalizePart(
      record && !stringValue(record, ["type", "part_kind", "kind"])
        ? { ...record, type: "tool-call" }
        : item
    );
    return part.type === "tool-call" ? [part] : [];
  });
  if (parts.length === 0) {
    return messages;
  }
  const last = messages.at(-1);
  if (last?.role === "assistant") {
    if (last.parts.some((part) => part.type === "tool-call")) {
      return messages;
    }
    return [
      ...messages.slice(0, -1),
      { ...last, parts: [...last.parts, ...parts] },
    ];
  }
  return [
    ...messages,
    { id: "assistant:tool-calls", parts, role: "assistant" },
  ];
};

export const aiMessages = (span: ServiceTraceSpan): AiMessage[] => {
  const attributes = otlpAttributeMap(span.span);
  const system = firstValue(attributes, [
    "gen_ai.system_instructions",
    "ai.prompt.system",
  ]);
  const allMessages = firstValue(attributes, ["pydantic_ai.all_messages"]);
  if (allMessages !== undefined) {
    return withSystem(normalizeMessages(allMessages, "message"), system);
  }
  const prompt = promptBag(
    firstValue(attributes, [
      "gen_ai.input.messages",
      "ai.prompt.messages",
      "ai.prompt",
    ])
  );
  const output = firstValue(attributes, [
    "gen_ai.output.messages",
    "ai.response.messages",
    "ai.response.text",
  ]);
  return withSystem(
    withResponseToolCalls(
      [
        ...normalizeMessages(prompt.messages, "user"),
        ...normalizeMessages(output, "assistant"),
      ],
      firstValue(attributes, ["ai.response.toolCalls"])
    ),
    system ?? prompt.system
  );
};

const normalizeToolDefinition = (
  value: unknown
): AiToolDefinition | undefined => {
  const record = asRecord(parsed(value));
  if (!record) {
    return;
  }
  const name = stringValue(record, ["name", "toolName", "tool_name", "id"]);
  if (!name) {
    return;
  }
  return {
    description: stringValue(record, ["description"]),
    name,
    parameters: parsed(
      record.parameters ??
        record.inputSchema ??
        record.input_schema ??
        record.args
    ),
    type: stringValue(record, ["type"]) ?? "function",
  };
};

export const aiToolDefinitions = (
  span: ServiceTraceSpan
): AiToolDefinition[] => {
  const attributes = otlpAttributeMap(span.span);
  const seen = new Set<string>();
  return asList(
    firstValue(attributes, ["gen_ai.tool.definitions", "ai.prompt.tools"])
  ).flatMap((item) => {
    const definition = normalizeToolDefinition(item);
    if (!definition || seen.has(definition.name)) {
      return [];
    }
    seen.add(definition.name);
    return [definition];
  });
};

export const uniqueAiToolDefinitions = (spans: ServiceTraceSpan[]) => {
  const seen = new Set<string>();
  return spans.flatMap(aiToolDefinitions).filter((definition) => {
    if (seen.has(definition.name)) {
      return false;
    }
    seen.add(definition.name);
    return true;
  });
};

const requestSettingFields: { keys: string[]; label: string }[] = [
  {
    keys: ["gen_ai.request.temperature", "ai.settings.temperature"],
    label: "Temperature",
  },
  {
    keys: [
      "gen_ai.request.max_tokens",
      "ai.settings.maxOutputTokens",
      "ai.settings.maxTokens",
    ],
    label: "Max tokens",
  },
  { keys: ["gen_ai.request.top_p", "ai.settings.topP"], label: "Top P" },
  { keys: ["gen_ai.request.top_k", "ai.settings.topK"], label: "Top K" },
  { keys: ["gen_ai.request.frequency_penalty"], label: "Frequency penalty" },
  { keys: ["gen_ai.request.presence_penalty"], label: "Presence penalty" },
  { keys: ["gen_ai.request.stop_sequences"], label: "Stop" },
  { keys: ["gen_ai.request.seed"], label: "Seed" },
  { keys: ["gen_ai.request.stream"], label: "Stream" },
  { keys: ["gen_ai.request.reasoning.level"], label: "Reasoning" },
  { keys: ["gen_ai.output.type"], label: "Output type" },
  { keys: ["ai.prompt.toolChoice"], label: "Tool choice" },
  {
    keys: ["gen_ai.response.finish_reasons", "ai.response.finishReason"],
    label: "Finish",
  },
  { keys: ["ai.settings.maxRetries"], label: "Max retries" },
];

export const aiRequestSettings = (span: ServiceTraceSpan): AiSettingRow[] => {
  const attributes = otlpAttributeMap(span.span);
  const rows: AiSettingRow[] = [];
  const add = (label: string, value: unknown) => {
    if (value === undefined || value === null || value === "") {
      return;
    }
    if (rows.some((row) => row.label === label)) {
      return;
    }
    rows.push({ label, value });
  };
  for (const field of requestSettingFields) {
    add(field.label, firstValue(attributes, field.keys));
  }
  for (const [key, value] of attributes) {
    if (
      !key.startsWith("ai.settings.") ||
      key.startsWith("ai.settings.context.") ||
      key.startsWith("ai.settings.runtimeContext.") ||
      isSensitiveAiAttributeKey(key)
    ) {
      continue;
    }
    add(key.slice("ai.settings.".length), parsed(value));
  }
  return rows;
};

export const mergedAiRequestSettings = (spans: ServiceTraceSpan[]) => {
  const rows: AiSettingRow[] = [];
  for (const span of spans) {
    for (const row of aiRequestSettings(span)) {
      if (!rows.some((existing) => existing.label === row.label)) {
        rows.push(row);
      }
    }
  }
  return rows;
};

export const aiTool = (span: ServiceTraceSpan) => {
  const attributes = otlpAttributeMap(span.span);
  return {
    input: firstValue(attributes, [
      "gen_ai.tool.call.arguments",
      "gen_ai.tool.arguments",
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
