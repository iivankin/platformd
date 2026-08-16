export interface Breadcrumb {
  category: string;
  data: Record<string, unknown>;
  level: string;
  message: string;
  timestamp?: string | number;
  type: string;
}

export type ContextGroup = [string, [string, string][]];

export type EnvironmentContextKind =
  | "browser"
  | "device"
  | "geo"
  | "os"
  | "runtime"
  | "user";

export interface EnvironmentContext {
  details: [string, string][];
  kind: EnvironmentContextKind;
  title: string;
}

export interface EventRequest {
  headers: [string, string][];
  method?: string;
  rows: [string, string][];
  url?: string;
}

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

const firstText = (...values: unknown[]) =>
  values.map(scalarText).find((value) => value !== undefined && value !== "");

const compactRows = (
  rows: [string, string | undefined][]
): [string, string][] =>
  rows.flatMap(([key, value]) => (value === undefined ? [] : [[key, value]]));

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

const namedContext = (
  kind: Exclude<EnvironmentContextKind, "geo" | "user">,
  value: unknown,
  title: string | undefined,
  omitted: string[]
): EnvironmentContext | undefined => {
  const details = recordRows(value, new Set(["type", ...omitted]));
  if (!title && details.length === 0) {
    return undefined;
  }
  return { details, kind, title: title ?? "Unknown" };
};

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

const userEnvironmentContext = (
  value: unknown
): EnvironmentContext | undefined => {
  const user = asRecord(value);
  if (!user) {
    return undefined;
  }
  const title = firstText(
    user.name,
    user.username,
    user.email,
    user.id,
    user.ip_address
  );
  const details = compactRows([
    ["Name", scalarText(user.name)],
    ["Username", scalarText(user.username)],
    ["Email", scalarText(user.email)],
    ["ID", scalarText(user.id)],
    ["IP address", scalarText(user.ip_address)],
  ]);
  const custom = recordRows(
    user,
    new Set(["name", "username", "email", "id", "ip_address", "geo"])
  );
  return title || details.length > 0 || custom.length > 0
    ? {
        details: [...details, ...custom],
        kind: "user",
        title: title ?? "Anonymous user",
      }
    : undefined;
};

const browserEnvironmentContext = (
  value: unknown
): EnvironmentContext | undefined => {
  const browser = asRecord(value);
  const browserName = firstText(browser?.name, browser?.browser);
  const browserVersion = firstText(browser?.version);
  return namedContext(
    "browser",
    browser,
    [browserName, browserVersion].filter(Boolean).join(" ") || undefined,
    ["name", "browser", "version"]
  );
};

const deviceEnvironmentContext = (
  value: unknown
): EnvironmentContext | undefined => {
  const device = asRecord(value);
  const deviceContext = namedContext(
    "device",
    device,
    firstText(device?.model, device?.name, device?.family, device?.brand),
    ["model", "name", "family", "brand"]
  );
  if (!deviceContext) {
    return undefined;
  }
  const identity = compactRows([
    ["Brand", scalarText(device?.brand)],
    ["Family", scalarText(device?.family)],
  ]);
  deviceContext.details.unshift(...identity);
  return deviceContext;
};

const versionedEnvironmentContext = (
  kind: "os" | "runtime",
  value: unknown
): EnvironmentContext | undefined => {
  const context = asRecord(value);
  const name = firstText(context?.name);
  const version = firstText(context?.version);
  return namedContext(
    kind,
    context,
    [name, version].filter(Boolean).join(" ") || undefined,
    ["name", "version"]
  );
};

const geoEnvironmentContext = (
  value: unknown
): EnvironmentContext | undefined => {
  const geo = asRecord(value);
  if (!geo) {
    return undefined;
  }
  const country = firstText(geo.country_code, geo.country);
  const region = firstText(geo.subdivision, geo.region);
  const city = firstText(geo.city);
  const details = compactRows([
    ["City", city],
    ["Region", region],
    ["Country", country],
  ]);
  const custom = recordRows(
    geo,
    new Set(["city", "subdivision", "region", "country_code", "country"])
  );
  const location = [...new Set([city, region, country].filter(Boolean))];
  return details.length > 0 || custom.length > 0
    ? {
        details: [...details, ...custom],
        kind: "geo",
        title: location.join(", ") || "Unknown",
      }
    : undefined;
};

const isEnvironmentContext = (
  context: EnvironmentContext | undefined
): context is EnvironmentContext => context !== undefined;

export const eventEnvironment = (payload: unknown): EnvironmentContext[] => {
  const event = asRecord(payload);
  if (!event) {
    return [];
  }
  const contexts = asRecord(event.contexts) ?? {};
  const user = asRecord(event.user);
  return [
    userEnvironmentContext(user),
    browserEnvironmentContext(contexts.browser),
    deviceEnvironmentContext(contexts.device),
    versionedEnvironmentContext("os", contexts.os),
    versionedEnvironmentContext("runtime", contexts.runtime),
    geoEnvironmentContext(asRecord(user?.geo) ?? contexts.geo),
  ].filter(isEnvironmentContext);
};

const headerRows = (value: unknown): [string, string][] => {
  if (Array.isArray(value)) {
    return value.flatMap((header): [string, string][] => {
      if (Array.isArray(header) && typeof header[0] === "string") {
        return [[header[0], displayValue(header[1])]];
      }
      const record = asRecord(header);
      const name = scalarText(record?.name ?? record?.key);
      return name ? [[name, displayValue(record?.value)]] : [];
    });
  }
  return recordRows(value);
};

export const eventRequest = (payload: unknown): EventRequest | undefined => {
  const request = asRecord(asRecord(payload)?.request);
  if (!request) {
    return undefined;
  }
  const headers = headerRows(request.headers);
  const method = scalarText(request.method)?.toUpperCase();
  const url = scalarText(request.url);
  const rows = recordRows(request, new Set(["headers", "method", "url"]));
  if (!method && !url && headers.length === 0 && rows.length === 0) {
    return undefined;
  }
  return { headers, method, rows, url };
};

export const eventContextGroups = (payload: unknown): ContextGroup[] => {
  const event = asRecord(payload);
  if (!event) {
    return [];
  }
  const groups: ContextGroup[] = [];
  const specialized = new Set([
    "browser",
    "device",
    "geo",
    "os",
    "replay",
    "runtime",
    "trace",
  ]);
  for (const [name, context] of Object.entries(
    asRecord(event.contexts) ?? {}
  )) {
    if (specialized.has(name.toLowerCase())) {
      continue;
    }
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
