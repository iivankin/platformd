export interface Breadcrumb {
  category: string;
  data: Record<string, unknown>;
  level: string;
  message: string;
  timestamp?: string | number;
  type: string;
}

export type ContextGroup = [string, [string, string][]];

export const asRecord = (value: unknown): Record<string, unknown> | undefined =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;

const scalarText = (value: unknown): string | undefined => {
  if (typeof value === "string") {
    return value;
  }
  if (typeof value === "number" || typeof value === "boolean") {
    return String(value);
  }
  return undefined;
};

export const displayValue = (value: unknown) => {
  const scalar = scalarText(value);
  if (scalar !== undefined) {
    return scalar;
  }
  if (value === null) {
    return "null";
  }
  try {
    const serialized = JSON.stringify(value);
    if (!serialized) {
      return "—";
    }
    return serialized.length > 320
      ? `${serialized.slice(0, 317)}…`
      : serialized;
  } catch {
    return "[unavailable]";
  }
};

export const recordRows = (
  value: unknown,
  omitted = new Set<string>()
): [string, string][] =>
  Object.entries(asRecord(value) ?? {}).flatMap(([key, entry]) =>
    omitted.has(key) || entry === undefined ? [] : [[key, displayValue(entry)]]
  );

export const eventTags = (payload: unknown): [string, string][] => {
  const tags = asRecord(payload)?.tags;
  if (Array.isArray(tags)) {
    return tags.flatMap((tag): [string, string][] => {
      if (Array.isArray(tag) && typeof tag[0] === "string") {
        return [[tag[0], displayValue(tag[1])]];
      }
      const record = asRecord(tag);
      return record && typeof record.key === "string"
        ? [[record.key, displayValue(record.value)]]
        : [];
    });
  }
  return recordRows(tags);
};

export const eventBreadcrumbs = (payload: unknown): Breadcrumb[] => {
  const breadcrumbs = asRecord(payload)?.breadcrumbs;
  const values = Array.isArray(breadcrumbs)
    ? breadcrumbs
    : asRecord(breadcrumbs)?.values;
  if (!Array.isArray(values)) {
    return [];
  }
  return values.flatMap((value): Breadcrumb[] => {
    const breadcrumb = asRecord(value);
    if (!breadcrumb) {
      return [];
    }
    return [
      {
        category: scalarText(breadcrumb.category) ?? "default",
        data: asRecord(breadcrumb.data) ?? {},
        level: scalarText(breadcrumb.level) ?? "info",
        message: scalarText(breadcrumb.message) ?? "",
        timestamp:
          typeof breadcrumb.timestamp === "string" ||
          typeof breadcrumb.timestamp === "number"
            ? breadcrumb.timestamp
            : undefined,
        type: scalarText(breadcrumb.type) ?? "default",
      },
    ];
  });
};

export const eventContextGroups = (payload: unknown): ContextGroup[] => {
  const event = asRecord(payload);
  if (!event) {
    return [];
  }
  const groups: ContextGroup[] = [];
  const user = recordRows(event.user);
  if (user.length > 0) {
    groups.push(["User", user]);
  }
  const request = recordRows(event.request);
  if (request.length > 0) {
    groups.push(["Request", request]);
  }
  for (const [name, context] of Object.entries(
    asRecord(event.contexts) ?? {}
  )) {
    const rows = recordRows(context, new Set(["type"]));
    if (rows.length > 0) {
      groups.push([name, rows]);
    }
  }
  const extra = recordRows(event.extra);
  if (extra.length > 0) {
    groups.push(["Extra", extra]);
  }
  return groups;
};
