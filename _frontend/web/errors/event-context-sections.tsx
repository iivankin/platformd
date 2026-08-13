import {
  Cpu,
  Globe2,
  Laptop,
  MapPin,
  MonitorSmartphone,
  Network,
  UserRound,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { Eyebrow } from "./common-ui";
import { eventEnvironment, eventRequest } from "./event-context";
import type { EnvironmentContextKind } from "./event-context";

const contextPresentation: Record<
  EnvironmentContextKind,
  { icon: LucideIcon; label: string }
> = {
  browser: { icon: Globe2, label: "Browser" },
  device: { icon: Laptop, label: "Device" },
  geo: { icon: MapPin, label: "Location" },
  os: { icon: MonitorSmartphone, label: "Operating system" },
  runtime: { icon: Cpu, label: "Runtime" },
  user: { icon: UserRound, label: "User" },
};

export const EventEnvironmentSection = ({ payload }: { payload: unknown }) => {
  const contexts = eventEnvironment(payload);
  if (contexts.length === 0) {
    return null;
  }
  return (
    <section className="border-b border-border py-6">
      <Eyebrow>User &amp; environment</Eyebrow>
      <p className="mt-1.5 text-[10px] text-muted-foreground">
        Identity and runtime context captured by the SDK.
      </p>
      <div className="mt-4 overflow-hidden border-t border-l border-border">
        <div className="grid grid-cols-3 max-lg:grid-cols-2 max-sm:grid-cols-1">
          {contexts.map((context) => {
            const presentation = contextPresentation[context.kind];
            const Icon = presentation.icon;
            let skippedTitle = false;
            const details = context.details.filter(([, value]) => {
              if (!skippedTitle && value === context.title) {
                skippedTitle = true;
                return false;
              }
              return true;
            });
            return (
              <div
                className="min-w-0 border-r border-b border-border px-4 py-4"
                key={context.kind}
              >
                <div className="flex items-center gap-2 text-[9px] font-medium tracking-[0.1em] text-muted-foreground uppercase">
                  <Icon className="size-3.5" />
                  {presentation.label}
                </div>
                <p className="mt-3 text-[11px] font-medium [overflow-wrap:anywhere]">
                  {context.title}
                </p>
                {details.length > 0 ? (
                  <dl className="mt-3 space-y-1.5">
                    {details.map(([key, value]) => (
                      <div
                        className="grid grid-cols-[72px_minmax(0,1fr)] gap-2 text-[9px]"
                        key={key}
                      >
                        <dt className="text-muted-foreground">{key}</dt>
                        <dd className="m-0 [overflow-wrap:anywhere] text-foreground/75">
                          {context.kind === "geo" &&
                          key === "source" &&
                          value === "https://db-ip.com" ? (
                            <a
                              className="underline underline-offset-2 hover:text-foreground"
                              href={value}
                              rel="noreferrer"
                              target="_blank"
                            >
                              IP geolocation by DB-IP
                            </a>
                          ) : (
                            value
                          )}
                        </dd>
                      </div>
                    ))}
                  </dl>
                ) : null}
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
};

export const EventRequestSection = ({ payload }: { payload: unknown }) => {
  const request = eventRequest(payload);
  if (!request) {
    return null;
  }
  return (
    <section className="border-b border-border py-6">
      <div className="flex items-center gap-2">
        <Network className="size-3.5 text-muted-foreground" />
        <Eyebrow>Request</Eyebrow>
      </div>
      <div className="mt-4 border-y border-border">
        <div className="flex min-w-0 items-start gap-3 py-3 text-[10px]">
          {request.method ? (
            <code className="shrink-0 border border-border px-2 py-1 text-[9px] font-medium">
              {request.method}
            </code>
          ) : null}
          <code className="min-w-0 pt-1 [overflow-wrap:anywhere] text-foreground/85">
            {request.url ?? "URL not provided"}
          </code>
        </div>
        {request.rows.map(([key, value]) => (
          <div
            className="grid grid-cols-[112px_minmax(0,1fr)] gap-3 border-t border-border py-2.5 text-[9px]"
            key={key}
          >
            <span className="text-muted-foreground">{key}</span>
            <code className="[overflow-wrap:anywhere] text-foreground/75">
              {value}
            </code>
          </div>
        ))}
      </div>
      {request.headers.length > 0 ? (
        <div className="mt-5">
          <p className="mb-2 text-[9px] font-medium tracking-[0.1em] text-muted-foreground uppercase">
            Headers
          </p>
          <dl className="divide-y divide-border border-y border-border">
            {request.headers.map(([key, value], index) => (
              <div
                className="grid grid-cols-[144px_minmax(0,1fr)] gap-3 py-2.5 text-[9px] max-sm:grid-cols-1 max-sm:gap-1"
                key={`${key}:${index}`}
              >
                <dt className="text-muted-foreground">{key}</dt>
                <dd className="m-0 [overflow-wrap:anywhere] text-foreground/75">
                  {value}
                </dd>
              </div>
            ))}
          </dl>
        </div>
      ) : null}
    </section>
  );
};
