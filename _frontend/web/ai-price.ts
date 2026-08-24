export interface AiPrice {
  estimated: boolean;
  model: string;
  partial?: boolean;
  provider: string;
  value: number;
}

export const storedAiPrice = ({
  actual,
  estimated,
  model,
  partial = false,
  provider,
}: {
  actual?: number | null;
  estimated?: number | null;
  model: string;
  partial?: boolean;
  provider: string;
}): AiPrice | undefined => {
  if (
    (actual === null || actual === undefined) &&
    (estimated === null || estimated === undefined)
  ) {
    return undefined;
  }
  return {
    estimated: estimated !== null && estimated !== undefined,
    model,
    partial,
    provider,
    value: (actual ?? 0) + (estimated ?? 0),
  };
};

export const formatAiCost = (value: number, estimated = false) => {
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
  return estimated ? `~${formatted}` : formatted;
};

export const formatAiPrice = (price: AiPrice) =>
  `${formatAiCost(price.value, price.estimated)}${price.partial ? "+" : ""}`;
