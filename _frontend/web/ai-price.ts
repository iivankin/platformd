import { calcPrice } from "@pydantic/genai-prices";

export interface AiUsage {
  cacheReadTokens?: number | null;
  cacheWriteTokens?: number | null;
  inputTokens?: number | null;
  outputTokens?: number | null;
}

export interface AiPrice {
  estimated: boolean;
  model: string;
  provider: string;
  value: number;
}

const PROVIDER_ALIASES: Record<string, string> = {
  "aws.bedrock": "aws-bedrock",
  azure_openai: "azure",
  bedrock: "aws-bedrock",
  "gcp.vertex_ai": "google-vertex",
  vertex_ai: "google-vertex",
};

export const calculateAiPrice = ({
  actual,
  model,
  provider,
  timestamp,
  usage,
}: {
  actual?: number | null;
  model: string;
  provider: string;
  timestamp?: Date;
  usage: AiUsage;
}): AiPrice | undefined => {
  if (actual !== null && actual !== undefined) {
    return { estimated: false, model, provider, value: actual };
  }
  if (!model || !(usage.inputTokens || usage.outputTokens)) {
    return undefined;
  }
  const providerID = (PROVIDER_ALIASES[provider] ?? provider) || undefined;
  const priceUsage = {
    cache_read_tokens: usage.cacheReadTokens ?? undefined,
    cache_write_tokens: usage.cacheWriteTokens ?? undefined,
    input_tokens: usage.inputTokens ?? undefined,
    output_tokens: usage.outputTokens ?? undefined,
  };
  try {
    const result = calcPrice(priceUsage, model, {
      providerId: providerID,
      timestamp,
    });
    const fallback = result ?? calcPrice(priceUsage, model, { timestamp });
    return fallback
      ? {
          estimated: true,
          model: fallback.model.name ?? fallback.model.id,
          provider: fallback.provider.name,
          value: fallback.total_price,
        }
      : undefined;
  } catch {
    return undefined;
  }
};

export const formatAiPrice = (price: AiPrice) => {
  const { value } = price;
  let formatted: string;
  if (value === 0) {
    formatted = "$0";
  } else if (value < 0.0001) {
    formatted = `$${value.toExponential(2)}`;
  } else if (value < 0.01) {
    formatted = `$${value.toFixed(5)}`;
  } else {
    formatted = `$${value.toFixed(3)}`;
  }
  return price.estimated ? `~${formatted}` : formatted;
};
