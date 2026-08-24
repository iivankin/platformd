const record = (value: unknown): Record<string, unknown> | undefined =>
  value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;

export interface OTLPAttribute {
  key: string;
  value: unknown;
}

export const otlpNativeValue = (value: unknown): unknown => {
  if (Array.isArray(value)) {
    return value.map(otlpNativeValue);
  }
  const wrapped = record(value);
  if (!wrapped) {
    return value;
  }
  for (const candidate of [
    "stringValue",
    "intValue",
    "doubleValue",
    "boolValue",
    "bytesValue",
  ]) {
    if (wrapped[candidate] !== undefined) {
      return wrapped[candidate];
    }
  }
  const array = record(wrapped.arrayValue)?.values;
  if (Array.isArray(array)) {
    return array.map(otlpNativeValue);
  }
  const pairs = record(wrapped.kvlistValue)?.values;
  if (Array.isArray(pairs)) {
    return Object.fromEntries(
      pairs.flatMap((pair) => {
        const entry = record(pair);
        return entry && typeof entry.key === "string"
          ? [[entry.key, otlpNativeValue(entry.value)] as const]
          : [];
      })
    );
  }
  if ("value" in wrapped) {
    return otlpNativeValue(wrapped.value);
  }
  return Object.fromEntries(
    Object.entries(wrapped).map(([key, entry]) => [key, otlpNativeValue(entry)])
  );
};

export const otlpValueText = (value: unknown): string => {
  const native = otlpNativeValue(value);
  if (native === null || native === undefined) {
    return "null";
  }
  if (typeof native !== "object") {
    return String(native);
  }
  return JSON.stringify(native);
};

export const otlpAttributes = (value: unknown): OTLPAttribute[] => {
  const attributes = record(value)?.attributes;
  const object = record(attributes);
  if (object) {
    return Object.entries(object).map(([key, entry]) => ({
      key,
      value: otlpNativeValue(entry),
    }));
  }
  if (!Array.isArray(attributes)) {
    return [];
  }
  return attributes.flatMap((item) => {
    const attribute = record(item);
    return attribute && typeof attribute.key === "string"
      ? [{ key: attribute.key, value: otlpNativeValue(attribute.value) }]
      : [];
  });
};

export const otlpTextAttributes = (value: unknown) =>
  otlpAttributes(value).map(({ key, value: entry }) => ({
    key,
    value: otlpValueText(entry),
  }));

export const otlpAttributeMap = (value: unknown) =>
  new Map(otlpAttributes(value).map(({ key, value: entry }) => [key, entry]));

export const otlpAttribute = (value: unknown, key: string) =>
  otlpAttributeMap(value).get(key);

export const otlpAttributeText = (value: unknown, key: string) => {
  const attribute = otlpAttribute(value, key);
  return attribute === undefined ? undefined : otlpValueText(attribute);
};
