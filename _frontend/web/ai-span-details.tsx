import { Wrench } from "lucide-react";
import { useMemo } from "react";

import { calculateAiPrice, formatAiPrice } from "@/ai-price";
import type { AiPrice } from "@/ai-price";
import {
  aiMessages,
  aiRuns,
  aiSpanLabel,
  aiTool,
  otlpAttributeMap,
} from "@/ai-trace";
import type { ServiceTraceSpan } from "@/api";

const integer = (value: string) => globalThis.BigInt(value);

const durationSeconds = (value: string) =>
  Number(integer(value)) / 1_000_000_000;

const formatTokens = (value: number) => new Intl.NumberFormat().format(value);

const priceForSpan = (span: ServiceTraceSpan) =>
  calculateAiPrice({
    actual: span.aiCostUsd,
    model: span.aiModel,
    provider: span.aiProvider,
    timestamp: new Date(Number(integer(span.startTimeUnixNano) / 1_000_000n)),
    usage: {
      cacheReadTokens: span.aiCacheReadTokens,
      cacheWriteTokens: span.aiCacheWriteTokens,
      inputTokens: span.aiInputTokens,
      outputTokens: span.aiOutputTokens,
    },
  });

const sumMetric = (
  spans: ServiceTraceSpan[],
  field: keyof ServiceTraceSpan
) => {
  let total = 0;
  for (const span of spans) {
    const value = span[field];
    if (typeof value === "number") {
      total += value;
    }
  }
  return total;
};

const runPrice = (
  root: ServiceTraceSpan,
  models: ServiceTraceSpan[]
): AiPrice | undefined => {
  if (root.aiCostUsd !== null) {
    return priceForSpan(root);
  }
  const prices = models
    .map(priceForSpan)
    .filter((price): price is AiPrice => price !== undefined);
  if (prices.length === 0) {
    return;
  }
  const only = prices.length === 1 ? prices[0] : undefined;
  return {
    estimated: prices.some((price) => price.estimated),
    model: only?.model ?? "multiple models",
    provider: only?.provider ?? "multiple providers",
    value: prices.reduce((total, price) => total + price.value, 0),
  };
};

const formattedValue = (value: unknown) => {
  if (typeof value === "string") {
    return value;
  }
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
};

const Content = ({ label, value }: { label: string; value: unknown }) => (
  <section className="border-t border-border">
    <p className="px-4 pt-3 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
      {label}
    </p>
    <pre className="max-h-72 overflow-auto px-4 py-3 font-mono text-[9px] leading-relaxed whitespace-pre-wrap text-foreground/85">
      {formattedValue(value)}
    </pre>
  </section>
);

const Conversation = ({ spans }: { spans: ServiceTraceSpan[] }) => {
  const conversations = spans.flatMap((span) => {
    const messages = aiMessages(span);
    return messages.length > 0 ? [{ messages, span }] : [];
  });
  if (conversations.length === 0) {
    return null;
  }
  return (
    <section className="border-t border-border">
      <p className="px-4 py-3 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
        Conversation
      </p>
      {conversations.map(({ messages, span }, conversationIndex) => (
        <div
          className="border-t border-border/70"
          key={`${span.spanId}:${conversationIndex.toString()}`}
        >
          {conversations.length > 1 ? (
            <p className="bg-muted/15 px-4 py-2 text-[8px] text-muted-foreground">
              {aiSpanLabel(span)}
            </p>
          ) : null}
          {messages.map((message) => (
            <div
              className="grid grid-cols-[5.5rem_minmax(0,1fr)] border-t border-border/60 text-[9px] first:border-t-0"
              key={`${span.spanId}:${message.id}`}
            >
              <p className="px-4 py-3 text-[8px] tracking-[0.08em] text-muted-foreground uppercase">
                {message.role}
              </p>
              <div className="min-w-0 border-l border-border/60">
                {message.parts.map((part, index) => (
                  <div
                    className="border-t border-border/50 px-4 py-3 first:border-t-0"
                    key={`${message.id}:${index.toString()}`}
                  >
                    {part.type === "reasoning" ? (
                      <p className="mb-1 text-[8px] text-violet-500 uppercase">
                        Reasoning
                      </p>
                    ) : null}
                    {part.type === "tool-call" ||
                    part.type === "tool-result" ? (
                      <p className="mb-1 flex items-center gap-1.5 text-[8px] text-violet-500 uppercase">
                        <Wrench className="size-3" /> {part.name ?? part.type}
                      </p>
                    ) : null}
                    <pre className="overflow-auto font-mono leading-relaxed whitespace-pre-wrap text-foreground/85">
                      {formattedValue(
                        part.content ?? part.input ?? part.output
                      )}
                    </pre>
                  </div>
                ))}
              </div>
            </div>
          ))}
        </div>
      ))}
    </section>
  );
};

const agentContext = (span: ServiceTraceSpan) =>
  [...otlpAttributeMap(span.span).entries()].filter(
    ([key]) =>
      (key.startsWith("ai.settings.context.") ||
        key.startsWith("ai.settings.runtimeContext.")) &&
      !key.toLowerCase().includes("secret")
  );

const uniqueLabel = (values: string[]) => {
  const unique = [...new Set(values.filter(Boolean))];
  if (unique.length === 0) {
    return "—";
  }
  return unique.join(", ");
};

interface AiSpanSummary {
  cacheRead: number;
  cacheWrite: number;
  input: number;
  model: string;
  modelCalls: number;
  output: number;
  price?: AiPrice;
  provider: string;
  reasoning: number;
  speed?: number;
  toolCalls: number;
  ttft?: number;
}

const summarize = (
  span: ServiceTraceSpan,
  traceSpans: ServiceTraceSpan[]
): {
  messageSpans: ServiceTraceSpan[];
  scope: ServiceTraceSpan[];
  summary: AiSpanSummary;
} => {
  const run =
    span.aiKind === "agent"
      ? aiRuns(traceSpans).find(
          (candidate) => candidate.root.spanId === span.spanId
        )
      : undefined;
  const scope = run?.spans ?? [span];
  const models = scope.filter((item) =>
    ["model", "embedding", "rerank"].includes(item.aiKind)
  );
  const metricSpans = span.aiKind === "agent" ? models : [span];
  const metric = (field: keyof ServiceTraceSpan) => {
    const own = span[field];
    return typeof own === "number" ? own : sumMetric(metricSpans, field);
  };
  const output = metric("aiOutputTokens");
  const totalModelSeconds = models.reduce(
    (total, model) => total + durationSeconds(model.durationNano),
    0
  );
  const ttftValues = models.flatMap((model) =>
    model.aiTtftSeconds === null ? [] : [model.aiTtftSeconds]
  );
  const candidates = models.length > 0 ? models : [span];
  const model = uniqueLabel(candidates.map((item) => item.aiModel));
  const provider = uniqueLabel(candidates.map((item) => item.aiProvider));
  const messageModels = models.filter((item) => aiMessages(item).length > 0);
  return {
    messageSpans:
      messageModels.length > 0
        ? messageModels
        : scope.filter((item) => aiMessages(item).length > 0),
    scope,
    summary: {
      cacheRead: metric("aiCacheReadTokens"),
      cacheWrite: metric("aiCacheWriteTokens"),
      input: metric("aiInputTokens"),
      model,
      modelCalls: models.length,
      output,
      price:
        span.aiKind === "agent" ? runPrice(span, models) : priceForSpan(span),
      provider,
      reasoning: metric("aiReasoningTokens"),
      speed:
        span.aiTokensPerSecond ??
        (output > 0 && totalModelSeconds > 0
          ? output / totalModelSeconds
          : undefined),
      toolCalls: scope.filter((item) => item.aiKind === "tool").length,
      ttft:
        span.aiTtftSeconds ??
        (ttftValues.length > 0 ? Math.min(...ttftValues) : undefined),
    },
  };
};

const summaryRows = (span: ServiceTraceSpan, summary: AiSpanSummary) => {
  const tokens = summary.input + summary.output;
  const rows = [
    ["Model", summary.model],
    ["Provider", summary.provider],
    ["Tokens", tokens > 0 ? formatTokens(tokens) : "—"],
    [
      "Input / output",
      summary.input > 0 || summary.output > 0
        ? `${formatTokens(summary.input)} / ${formatTokens(summary.output)}`
        : "—",
    ],
    [
      "Cache read / write",
      summary.cacheRead > 0 || summary.cacheWrite > 0
        ? `${formatTokens(summary.cacheRead)} / ${formatTokens(summary.cacheWrite)}`
        : "—",
    ],
    [
      "Reasoning",
      summary.reasoning > 0 ? formatTokens(summary.reasoning) : "—",
    ],
    ["Cost", summary.price ? formatAiPrice(summary.price) : "—"],
    ["TTFT", summary.ttft === undefined ? "—" : `${summary.ttft.toFixed(2)} s`],
    [
      "Speed",
      summary.speed === undefined ? "—" : `${summary.speed.toFixed(1)} tok/s`,
    ],
  ];
  if (span.aiKind === "agent") {
    rows.splice(2, 0, [
      "Calls",
      `${summary.modelCalls.toString()} model · ${summary.toolCalls.toString()} tool`,
    ]);
  }
  return rows;
};

const sectionLabel = (kind: string) => {
  if (kind === "agent") {
    return "AI run";
  }
  if (kind === "tool") {
    return "Tool call";
  }
  if (kind === "step") {
    return "Agent step";
  }
  return "Model call";
};

const ToolDetails = ({ span }: { span: ServiceTraceSpan }) => {
  const tool = aiTool(span);
  return (
    <section className="border-t border-border">
      <p className="flex items-center gap-1.5 px-4 py-3 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
        <Wrench className="size-3" /> {String(tool.name)}
      </p>
      {tool.input === undefined ? null : (
        <Content label="Input" value={tool.input} />
      )}
      {tool.output === undefined ? null : (
        <Content label="Result" value={tool.output} />
      )}
    </section>
  );
};

export const AiSpanDetails = ({
  span,
  traceSpans,
}: {
  span: ServiceTraceSpan;
  traceSpans: ServiceTraceSpan[];
}) => {
  const { messageSpans, scope, summary } = useMemo(
    () => summarize(span, traceSpans),
    [span, traceSpans]
  );
  const context = agentContext(span);
  const tools = scope.filter((item) => item.aiKind === "tool");
  const rows = summaryRows(span, summary);
  if (span.aiKind === "tool") {
    return <ToolDetails span={span} />;
  }
  return (
    <div>
      <div className="border-t border-border">
        <p className="px-4 py-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
          {sectionLabel(span.aiKind)}
        </p>
        <dl className="grid grid-cols-2 border-t border-border/70">
          {rows.map(([label, value]) => (
            <div
              className="min-w-0 border-r border-b border-border/70 px-4 py-2 even:border-r-0"
              key={label}
            >
              <dt className="text-[8px] text-muted-foreground">{label}</dt>
              <dd
                className="mt-0.5 truncate text-[9px] tabular-nums"
                title={value}
              >
                {value}
              </dd>
            </div>
          ))}
        </dl>
        {summary.price?.estimated ? (
          <p className="px-4 py-2 text-[8px] text-muted-foreground">
            Cost estimated for {summary.price.provider} · {summary.price.model}.
          </p>
        ) : null}
      </div>
      <Conversation spans={messageSpans} />
      {tools.map((toolSpan) => (
        <ToolDetails key={toolSpan.spanId} span={toolSpan} />
      ))}
      {context.length > 0 ? (
        <Content label="Agent context" value={Object.fromEntries(context)} />
      ) : null}
    </div>
  );
};
