import { useState } from "react";

import { analyticsCookieDomain } from "@/analytics-model";
import type { AnalyticsMode, AnalyticsTracker } from "@/api";
import { cn } from "@/lib/utils";

import { CopyButton } from "./errors/settings-common";
import { HighlightedSnippet } from "./snippet-code";
import type { SnippetLanguage } from "./snippet-code";

/* Helpers are used by the exported guide below. */
/* eslint-disable no-use-before-define */

const tabsForMode = (mode: AnalyticsMode) =>
  mode === "cookieless"
    ? (["script"] as const)
    : (["script", "react", "ssr", "server"] as const);

export const AnalyticsSetupGuide = ({
  tracker,
}: {
  tracker: AnalyticsTracker;
}) => {
  const tabs = tabsForMode(tracker.mode);
  const [tab, setTab] = useState<"script" | "react" | "ssr" | "server">(
    "script"
  );
  const active = tracker.mode === "cookieless" ? "script" : tab;
  const source = snippet(tracker, active);

  return (
    <section className="border-b border-border px-5 py-6">
      <h2 className="text-sm font-medium">Install</h2>
      <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
        Script tag on every page. OpenFeature snippets use this tracker&apos;s
        origin and internal URL. React, SSR, and Server are hidden in cookieless
        mode because there is no anonymous identity.
      </p>
      <div className="mt-4 flex gap-1 border-b border-border">
        {tabs.map((id) => (
          <button
            className={cn(
              "px-3 py-2 text-[9px] tracking-[0.1em] text-muted-foreground uppercase",
              active === id && "text-foreground"
            )}
            key={id}
            onClick={() => setTab(id)}
            type="button"
          >
            {id}
          </button>
        ))}
      </div>
      {installFor(active) ? (
        <p className="mt-3 font-mono text-[10px] text-muted-foreground">
          {installFor(active)}
        </p>
      ) : null}
      <div className="relative mt-3">
        <HighlightedSnippet
          className="max-h-80"
          language={languageFor(active)}
          value={source}
        />
        <div className="absolute top-2 right-2">
          <CopyButton value={source} />
        </div>
      </div>
    </section>
  );
};

const languageFor = (
  tab: "script" | "react" | "ssr" | "server"
): SnippetLanguage => {
  if (tab === "script") {
    return "html";
  }
  if (tab === "server") {
    return "typescript";
  }
  return "tsx";
};

const installFor = (tab: "script" | "react" | "ssr" | "server") => {
  if (tab === "react") {
    return "npm install @openfeature/react-sdk @openfeature/ofrep-web-provider";
  }
  if (tab === "ssr") {
    return "npm install @openfeature/server-sdk @openfeature/ofrep-provider";
  }
  if (tab === "server") {
    return "npm install @openfeature/server-sdk @openfeature/ofrep-provider";
  }
  return "";
};

const snippet = (
  tracker: AnalyticsTracker,
  tab: "script" | "react" | "ssr" | "server"
) => {
  if (tab === "script") {
    return `<script defer src="/analytics.js"></script>`;
  }
  if (tab === "react") {
    return `import { OpenFeature, OpenFeatureProvider, useFlag } from "@openfeature/react-sdk";
import { OFREPWebProvider } from "@openfeature/ofrep-web-provider";

function bootOpenFeature() {
  const aid = platformd.anonymousId();
  if (!aid) {
    return;
  }
  OpenFeature.setContext({ targetingKey: aid });
  OpenFeature.addHooks(platformd.openFeatureHook());
  OpenFeature.setProvider(new OFREPWebProvider({ baseUrl: location.origin }));
}

bootOpenFeature();
window.addEventListener("platformd:consent", bootOpenFeature);

export function App({ children }: { children: React.ReactNode }) {
  return platformd.anonymousId()
    ? <OpenFeatureProvider>{children}</OpenFeatureProvider>
    : children;
}

export function PricingCta() {
  const flag = useFlag("pricing-v2", false);
  return flag.value ? <NewCta /> : <OldCta />;
}`;
  }
  if (tab === "ssr") {
    const grantCheck =
      tracker.mode === "opt-in"
        ? `
  if (request.cookies.get("platformd_consent")?.value !== "granted") {
    return NextResponse.next();
  }`
        : "";
    const cookieDomain = analyticsCookieDomain(tracker.rootDomain);
    const domainLine = cookieDomain ? `\n      domain: "${cookieDomain}",` : "";
    return `// middleware.ts — mode=${tracker.mode}
import { NextRequest, NextResponse } from "next/server";

export function middleware(request: NextRequest) {
  if (
    request.headers.get("sec-gpc") === "1" ||
    request.cookies.get("platformd_consent")?.value === "denied"
  ) {
    return NextResponse.next();
  }${grantCheck}
  const existing = request.cookies.get("platformd_aid")?.value;
  const id = existing ?? crypto.randomUUID();
  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("x-platformd-aid", id);
  const response = NextResponse.next({ request: { headers: requestHeaders } });
  if (!existing) {
    response.cookies.set("platformd_aid", id, {${domainLine}
      path: "/",
      maxAge: 31536000,
      secure: request.nextUrl.protocol === "https:",
      sameSite: "lax",
    });
  }
  return response;
}

// page.tsx — server
import { headers } from "next/headers";
import { OpenFeature } from "@openfeature/server-sdk";
import { OFREPProvider } from "@openfeature/ofrep-provider";

OpenFeature.setProvider(new OFREPProvider({
  baseUrl: "${tracker.internalOfrepUrl}",
}));

export async function Page() {
  const targetingKey = (await headers()).get("x-platformd-aid");
  const pricingV2 = targetingKey
    ? await OpenFeature.getClient().getBooleanValue("pricing-v2", false, { targetingKey })
    : false;
  return <PricingCta pricingV2={pricingV2} />;
}`;
  }
  return `import { OpenFeature } from "@openfeature/server-sdk";
import { OFREPProvider } from "@openfeature/ofrep-provider";

OpenFeature.setProvider(new OFREPProvider({
  baseUrl: "${tracker.internalOfrepUrl}",
}));

export async function pricingEnabled(aid: string | undefined) {
  if (!aid) {
    return false;
  }
  return OpenFeature.getClient().getBooleanValue("pricing-v2", false, {
    targetingKey: aid,
  });
}`;
};
