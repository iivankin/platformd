export type AnalyticsMode = "cookieless" | "opt-in" | "opt-out";
export type AnalyticsConsent = "denied" | "granted";

export type AnalyticsValue =
  | boolean
  | null
  | number
  | string
  | readonly AnalyticsValue[]
  | { readonly [key: string]: AnalyticsValue };

export type AnalyticsProperties = Readonly<Record<string, AnalyticsValue>>;
export type AnalyticsEventMap = Record<string, AnalyticsProperties>;

export interface CreateAnalyticsOptions {
  /** Domain shared by the consent, anonymous ID, and session cookies. */
  cookieDomain?: string;
  mode: AnalyticsMode;
}

export interface OpenFeatureEvaluationContext {
  flagKey?: string;
}

export interface OpenFeatureEvaluationDetails {
  errorCode?: string;
  reason?: string;
  variant?: string;
}

export interface AnalyticsOpenFeatureHook {
  after: (
    context: OpenFeatureEvaluationContext,
    details: OpenFeatureEvaluationDetails
  ) => void;
}

export interface AnalyticsClient<Events extends AnalyticsEventMap> {
  anonymousId: () => string | null;
  consent: (value: AnalyticsConsent) => void;
  destroy: () => void;
  openFeatureHook: () => AnalyticsOpenFeatureHook;
  track: <Name extends keyof Events & string>(
    name: Name,
    properties?: Events[Name]
  ) => void;
}

interface BrowserEvent {
  i: boolean;
  l: string;
  n: string;
  p: AnalyticsProperties;
  r: string;
  s: string | null;
  t: string;
  u: string;
  w: string;
}

const aidCookie = "platformd_aid";
const consentCookie = "platformd_consent";
const sidCookie = "platformd_sid";

const readCookie = (name: string) => {
  const match = document.cookie.match(
    new RegExp(`(?:^|; )${name}=([^;]*)`, "u")
  );
  return match?.[1] ? decodeURIComponent(match[1]) : null;
};

const writeCookie = (
  name: string,
  value: string,
  maxAge: number,
  domain: string | undefined
) => {
  let cookie = `${name}=${encodeURIComponent(value)};path=/;SameSite=Lax;max-age=${maxAge}`;
  if (location.protocol === "https:") {
    cookie += ";Secure";
  }
  if (domain) {
    cookie += `;Domain=${domain}`;
  }
  // Cookie Store is not available in all browsers supported by the SDK, and
  // these first-party cookies must be written synchronously before an event.
  // eslint-disable-next-line unicorn/no-document-cookie
  document.cookie = cookie;
};

const uuid = () => crypto.randomUUID();

const globalPrivacyControl = () =>
  (navigator as Navigator & { globalPrivacyControl?: boolean })
    .globalPrivacyControl === true;

const deliverWithFetch = async (blob: Blob) => {
  try {
    await fetch("/analytics/e", {
      body: blob,
      credentials: "same-origin",
      keepalive: true,
      method: "POST",
    });
  } catch {
    // Analytics delivery must never surface a network failure to the app.
  }
};

/**
 * Starts browser analytics immediately and installs pageview, heatmap, and SPA
 * navigation instrumentation. Initialize it once in the application's browser
 * entry module; call destroy only when intentionally replacing the client.
 */
export const createAnalytics = <
  Events extends AnalyticsEventMap = AnalyticsEventMap,
>({
  cookieDomain,
  mode,
}: CreateAnalyticsOptions): AnalyticsClient<Events> => {
  if (typeof window === "undefined" || typeof document === "undefined") {
    throw new TypeError(
      "@platformd/analytics must be initialized in a browser"
    );
  }

  const denied = () => readCookie(consentCookie) === "denied";
  const cookieless = () => mode === "cookieless" || globalPrivacyControl();
  const granted = () => readCookie(consentCookie) === "granted";
  const allowed = () => !denied() && (mode !== "opt-in" || granted());
  const anonymousId = () => {
    if (cookieless() || denied() || (mode === "opt-in" && !granted())) {
      return null;
    }
    const existing = readCookie(aidCookie);
    if (existing) {
      return existing;
    }
    const id = uuid();
    writeCookie(aidCookie, id, 31_536_000, cookieDomain);
    return id;
  };
  const sessionId = () => {
    if (cookieless() || !allowed()) {
      return null;
    }
    const existing = readCookie(sidCookie);
    if (existing) {
      writeCookie(sidCookie, existing, 1800, cookieDomain);
      return existing;
    }
    const id = uuid();
    writeCookie(sidCookie, id, 1800, cookieDomain);
    return id;
  };
  const send = (name: string, properties: AnalyticsProperties = {}) => {
    if (!allowed()) {
      return;
    }
    if (!cookieless() && !anonymousId()) {
      return;
    }
    const body: BrowserEvent = {
      i: properties.interactive === true,
      l: navigator.language,
      n: name,
      p: properties,
      r: document.referrer,
      s: sessionId(),
      t: document.title,
      u: location.href,
      w: `${screen.width}x${screen.height}`,
    };
    const blob = new Blob([JSON.stringify(body)], { type: "application/json" });
    if (navigator.sendBeacon?.("/analytics/e", blob)) {
      return;
    }
    void deliverWithFetch(blob);
  };
  const track = <Name extends keyof Events & string>(
    name: Name,
    properties?: Events[Name]
  ) => send(name, properties ?? {});
  const pageview = () => {
    if (document.visibilityState !== "hidden") {
      send("$pageview", { interactive: true });
    }
  };
  const pageleave = () => send("$pageleave");
  const heatmapClick = (event: MouseEvent) => {
    const root = document.documentElement;
    const width = Math.max(root.scrollWidth, 1);
    const height = Math.max(root.scrollHeight, 1);
    send("$heatmap", {
      event_type: "click",
      page_h: height,
      viewport_h: window.innerHeight,
      viewport_w: window.innerWidth,
      x: Math.max(0, Math.min(100, Math.round((event.pageX / width) * 100))),
      y: Math.max(0, Math.min(100, Math.round((event.pageY / height) * 100))),
    });
  };
  let lastScroll = 0;
  const heatmapScroll = () => {
    const now = Date.now();
    if (now - lastScroll < 800) {
      return;
    }
    lastScroll = now;
    const root = document.documentElement;
    const span = Math.max(root.scrollHeight - window.innerHeight, 1);
    const percentage = Math.max(
      0,
      Math.min(100, Math.round((window.scrollY / span) * 100))
    );
    send("$heatmap", {
      event_type: "scroll",
      page_h: root.scrollHeight,
      scroll_pct: percentage,
      viewport_h: window.innerHeight,
      viewport_w: window.innerWidth,
      x: 50,
      y: percentage,
    });
  };
  let lastLocation = location.pathname + location.search;
  const routePageview = () => {
    const nextLocation = location.pathname + location.search;
    if (lastLocation === nextLocation) {
      return;
    }
    lastLocation = nextLocation;
    pageview();
  };
  const originalPushState = history.pushState;
  const originalReplaceState = history.replaceState;
  const pushState: History["pushState"] = function pushState(
    this: History,
    ...arguments_
  ) {
    originalPushState.apply(this, arguments_);
    routePageview();
  };
  const replaceState: History["replaceState"] = function replaceState(
    this: History,
    ...arguments_
  ) {
    originalReplaceState.apply(this, arguments_);
    routePageview();
  };
  history.pushState = pushState;
  history.replaceState = replaceState;
  window.addEventListener("pagehide", pageleave);
  window.addEventListener("popstate", routePageview);
  document.addEventListener("click", heatmapClick, true);
  window.addEventListener("scroll", heatmapScroll, { passive: true });
  pageview();

  return {
    anonymousId,
    consent(value) {
      writeCookie(consentCookie, value, 31_536_000, cookieDomain);
      if (value === "denied") {
        writeCookie(aidCookie, "", 0, cookieDomain);
        writeCookie(sidCookie, "", 0, cookieDomain);
        return;
      }
      pageview();
    },
    destroy() {
      window.removeEventListener("pagehide", pageleave);
      window.removeEventListener("popstate", routePageview);
      document.removeEventListener("click", heatmapClick, true);
      window.removeEventListener("scroll", heatmapScroll);
      if (history.pushState === pushState) {
        history.pushState = originalPushState;
      }
      if (history.replaceState === replaceState) {
        history.replaceState = originalReplaceState;
      }
    },
    openFeatureHook() {
      return {
        after(context, details) {
          if (
            details.errorCode ||
            (details.reason && details.reason !== "TARGETING_MATCH")
          ) {
            return;
          }
          send("$flag_called", {
            flag: context.flagKey ?? "",
            variant: details.variant ?? "",
          });
        },
      };
    },
    track,
  };
};
