import { Activity, Circle, Globe2, MousePointer2, Network } from "lucide-react";

import { Eyebrow, StatusBadge } from "./common-ui";
import {
  displayValue,
  eventBreadcrumbs,
  eventContextGroups,
  eventTags,
  recordRows,
} from "./event-context";

const breadcrumbTime = (value?: number | string) => {
  if (typeof value === "number") {
    const milliseconds = value < 1_000_000_000_000 ? value * 1000 : value;
    return new Date(milliseconds).toLocaleTimeString();
  }
  if (typeof value === "string") {
    const parsed = new Date(value);
    return Number.isNaN(parsed.valueOf()) ? value : parsed.toLocaleTimeString();
  }
  return "—";
};

const BreadcrumbIcon = ({
  category,
  type,
}: {
  category: string;
  type: string;
}) => {
  if (type === "navigation") {
    return <Globe2 />;
  }
  if (
    type === "http" ||
    category.includes("fetch") ||
    category.includes("xhr")
  ) {
    return <Network />;
  }
  if (category.startsWith("ui.")) {
    return <MousePointer2 />;
  }
  if (category === "console") {
    return <Activity />;
  }
  return <Circle />;
};

export const BreadcrumbsSection = ({ payload }: { payload: unknown }) => {
  const breadcrumbs = eventBreadcrumbs(payload);
  if (breadcrumbs.length === 0) {
    return null;
  }
  return (
    <section className="border-b border-border py-6">
      <div className="mb-4 flex items-end justify-between gap-4">
        <div>
          <Eyebrow>Breadcrumbs</Eyebrow>
          <p className="mt-1.5 text-[10px] text-muted-foreground">
            Actions and logs leading up to the failure.
          </p>
        </div>
        <span className="text-[9px] text-muted-foreground">
          {breadcrumbs.length.toLocaleString()} entries
        </span>
      </div>
      <div className="divide-y divide-border border-y border-border">
        {breadcrumbs.map((breadcrumb, index) => {
          const data = recordRows(breadcrumb.data);
          return (
            <div
              className="grid grid-cols-[24px_72px_minmax(0,1fr)_auto] gap-3 py-3 text-[10px] max-sm:grid-cols-[24px_minmax(0,1fr)_auto]"
              key={`${String(breadcrumb.timestamp)}:${breadcrumb.category}:${index}`}
            >
              <span className="mt-0.5 text-muted-foreground [&_svg]:size-3.5">
                <BreadcrumbIcon
                  category={breadcrumb.category}
                  type={breadcrumb.type}
                />
              </span>
              <code className="pt-0.5 text-[9px] text-muted-foreground max-sm:hidden">
                {breadcrumb.category}
              </code>
              <div className="min-w-0">
                <p className="[overflow-wrap:anywhere]">
                  {breadcrumb.message || breadcrumb.type}
                </p>
                {data.length > 0 ? (
                  <p className="mt-1 overflow-hidden text-[9px] text-ellipsis whitespace-nowrap text-muted-foreground">
                    {data
                      .slice(0, 3)
                      .map(([key, value]) => `${key}=${value}`)
                      .join(" · ")}
                  </p>
                ) : null}
              </div>
              <div className="flex items-start gap-2">
                <StatusBadge value={breadcrumb.level} />
                <time className="pt-0.5 text-[9px] text-muted-foreground tabular-nums">
                  {breadcrumbTime(breadcrumb.timestamp)}
                </time>
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
};

const ContextRows = ({ rows }: { rows: [string, string][] }) => (
  <dl className="divide-y divide-border border-y border-border">
    {rows.map(([key, value]) => (
      <div
        className="grid grid-cols-[112px_minmax(0,1fr)] gap-3 py-2 text-[9px]"
        key={key}
      >
        <dt className="text-muted-foreground">{key}</dt>
        <dd className="m-0 [overflow-wrap:anywhere] text-foreground/80">
          {value}
        </dd>
      </div>
    ))}
  </dl>
);

export const ContextSection = ({ payload }: { payload: unknown }) => {
  const tags = eventTags(payload);
  const groups = eventContextGroups(payload);
  if (tags.length === 0 && groups.length === 0) {
    return null;
  }
  return (
    <section className="py-6">
      <Eyebrow>Additional context</Eyebrow>
      <p className="mt-1.5 text-[10px] text-muted-foreground">
        Tags, custom contexts, and extra values merged into the event scope.
      </p>
      {tags.length > 0 ? (
        <div className="mt-5">
          <p className="mb-2 text-[9px] font-medium tracking-[0.1em] text-muted-foreground uppercase">
            Tags
          </p>
          <div className="flex flex-wrap gap-px bg-border p-px">
            {tags.map(([key, value]) => (
              <span
                className="bg-background px-2.5 py-1.5 text-[9px]"
                key={key}
              >
                <span className="text-muted-foreground">{key}</span>
                <span className="mx-1.5 text-border">/</span>
                <span>{value}</span>
              </span>
            ))}
          </div>
        </div>
      ) : null}
      <div className="mt-5 grid grid-cols-2 gap-x-8 gap-y-6 max-lg:grid-cols-1">
        {groups.map(([name, rows]) => (
          <div className="min-w-0" key={name}>
            <p className="mb-2 text-[9px] font-medium tracking-[0.1em] text-muted-foreground uppercase">
              {name}
            </p>
            <ContextRows rows={rows} />
          </div>
        ))}
      </div>
    </section>
  );
};

export const EventMessageSection = ({ payload }: { payload: unknown }) => {
  const rows = recordRows(
    payload,
    new Set([
      "exception",
      "breadcrumbs",
      "contexts",
      "extra",
      "request",
      "sdk",
      "tags",
      "user",
    ])
  );
  const highlights = rows.filter(([key]) =>
    [
      "dist",
      "environment",
      "logger",
      "release",
      "server_name",
      "transaction",
    ].includes(key)
  );
  if (highlights.length === 0) {
    return null;
  }
  return (
    <section className="border-b border-border py-6">
      <Eyebrow>Event attributes</Eyebrow>
      <div className="mt-4 max-w-3xl">
        <ContextRows
          rows={highlights.map(([key, value]) => [key, displayValue(value)])}
        />
      </div>
    </section>
  );
};
