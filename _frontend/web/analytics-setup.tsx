import { ExternalLink } from "lucide-react";
import { useState } from "react";

import { analyticsCookieDomain } from "@/analytics-model";
import type { AnalyticsTracker } from "@/api";
import { cn } from "@/lib/utils";

import { CopyButton } from "./errors/settings-common";
import { HighlightedSnippet } from "./snippet-code";
import type { SnippetLanguage } from "./snippet-code";

/* Helpers are used by the exported guide below. */
/* eslint-disable no-use-before-define */

const CLOUDFLARE_LOCATION_HEADERS_URL =
  "https://developers.cloudflare.com/rules/transform/managed-transforms/reference/#add-visitor-location-headers";

const tabs = ["browser", "react", "ssr", "server"] as const;

export const AnalyticsSetupGuide = ({
  tracker,
}: {
  tracker: AnalyticsTracker;
}) => {
  const [tab, setTab] = useState<"browser" | "react" | "ssr" | "server">(
    "browser"
  );
  const source = snippet(tracker, tab);

  return (
    <section className="border-b border-border px-5 py-6">
      <h2 className="text-sm font-medium">Install</h2>
      <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
        Initialize the typed browser package once in the application entry
        module. Choose cookieless, opt-in, or opt-out in application code, then
        rebuild after changing that choice or the root domain. SSR and
        OpenFeature require an anonymous ID, so they do not apply to cookieless
        traffic. Global Privacy Control forces cookieless delivery.
      </p>
      <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
        Visitor city and region require Cloudflare Rules → Settings → Managed
        Transforms →{" "}
        <a
          className="inline-flex items-center gap-1 text-foreground underline underline-offset-4"
          href={CLOUDFLARE_LOCATION_HEADERS_URL}
          rel="noreferrer"
          target="_blank"
        >
          Add visitor location headers
          <ExternalLink aria-hidden="true" className="size-2.5" />
        </a>
        . Without it, only the country may be available.
      </p>
      <div className="mt-4 flex gap-1 border-b border-border">
        {tabs.map((id) => (
          <button
            className={cn(
              "px-3 py-2 text-[9px] tracking-[0.1em] text-muted-foreground uppercase",
              tab === id && "text-foreground"
            )}
            key={id}
            onClick={() => setTab(id)}
            type="button"
          >
            {id}
          </button>
        ))}
      </div>
      {installFor(tab) ? (
        <p className="mt-3 font-mono text-[10px] text-muted-foreground">
          {installFor(tab)}
        </p>
      ) : null}
      <div className="relative mt-3">
        <HighlightedSnippet
          className="max-h-80"
          language={languageFor(tab)}
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
  tab: "browser" | "react" | "ssr" | "server"
): SnippetLanguage => {
  if (tab === "server") {
    return "typescript";
  }
  return tab === "browser" ? "typescript" : "tsx";
};

const installFor = (tab: "browser" | "react" | "ssr" | "server") => {
  if (tab === "browser") {
    return "npm install @platformd/analytics";
  }
  if (tab === "react") {
    return "npm install @platformd/analytics @openfeature/react-sdk @openfeature/ofrep-web-provider";
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
  tab: "browser" | "react" | "ssr" | "server"
) => {
  const cookieDomain = analyticsCookieDomain(tracker.rootDomain);
  const cookieDomainLine = cookieDomain
    ? `\n  cookieDomain: "${cookieDomain}",`
    : "";
  if (tab === "browser") {
    return `// analytics.ts — import once from the browser entry module.
import { createAnalytics } from "@platformd/analytics";

type AnalyticsEvents = {
  checkout_completed: {
    currency: string;
    orderId: string;
    revenue: number;
  };
};

export const analytics = createAnalytics<AnalyticsEvents>({${cookieDomainLine}
  mode: "opt-out", // Or "opt-in" / "cookieless".
});

analytics.track("checkout_completed", {
  currency: "USD",
  orderId: "order-1",
  revenue: 49.99,
});`;
  }
  if (tab === "react") {
    return `// analytics.ts — initialized once, outside React components.
import { createAnalytics } from "@platformd/analytics";
import { OpenFeature, OpenFeatureProvider, useFlag } from "@openfeature/react-sdk";
import { OFREPWebProvider } from "@openfeature/ofrep-web-provider";

export const analytics = createAnalytics({${cookieDomainLine}
  mode: "opt-out", // Or "opt-in" / "cookieless".
});

let openFeatureStarted = false;
function bootOpenFeature() {
  const aid = analytics.anonymousId();
  if (!aid || openFeatureStarted) {
    return;
  }
  openFeatureStarted = true;
  OpenFeature.setContext({ targetingKey: aid });
  OpenFeature.addHooks(analytics.openFeatureHook());
  OpenFeature.setProvider(new OFREPWebProvider({ baseUrl: location.origin }));
}

bootOpenFeature();

export function grantAnalyticsConsent() {
  analytics.consent("granted");
  bootOpenFeature();
}

export function App({ children }: { children: React.ReactNode }) {
  return analytics.anonymousId()
    ? <OpenFeatureProvider>{children}</OpenFeatureProvider>
    : children;
}

export function PricingCta() {
  const flag = useFlag("pricing-v2", false);
  return flag.value ? <NewCta /> : <OldCta />;
}`;
  }
  if (tab === "ssr") {
    const ssrCookieDomain = analyticsCookieDomain(tracker.rootDomain);
    const domainLine = ssrCookieDomain
      ? `\n      domain: "${ssrCookieDomain}",`
      : "";
    return `// middleware.ts — identified opt-out example.
// For opt-in, also require platformd_consent=granted. Cookieless needs no middleware.
import { NextRequest, NextResponse } from "next/server";

export function middleware(request: NextRequest) {
  if (
    request.headers.get("sec-gpc") === "1" ||
    request.cookies.get("platformd_consent")?.value === "denied"
  ) {
    return NextResponse.next();
  }
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
