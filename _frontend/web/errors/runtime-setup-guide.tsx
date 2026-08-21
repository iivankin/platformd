import { useState } from "react";

import { browserSentryInstall, browserSentrySetup } from "@/browser-otel-setup";
import { cn } from "@/lib/utils";
import { HighlightedSnippet } from "@/snippet-code";
import type { SnippetLanguage } from "@/snippet-code";

import { CopyButton } from "./settings-common";
import type { App } from "./types";

interface RuntimeGuide {
  id: string;
  install: string;
  label: string;
  language: SnippetLanguage;
  note: string;
  scope: "browser" | "server";
  source: (dsn: string, tunnel?: string) => string;
}

const quoted = (value: string) => JSON.stringify(value);

const publicTunnelURL = (app: App) => {
  if (!(app.publicDsn && app.browserTunnelPath)) {
    return;
  }
  return new URL(
    app.browserTunnelPath,
    new URL(app.publicDsn).origin
  ).toString();
};

const runtimeGuides: RuntimeGuide[] = [
  {
    id: "browser",
    install: browserSentryInstall,
    label: "Browser / React",
    language: "typescript",
    note: "Configure Browser OTLP traces above first. OTel owns tracing; Sentry sends errors and replay and attaches errors to the active OTel span.",
    scope: "browser",
    source: browserSentrySetup,
  },
  {
    id: "node",
    install: "npm install @sentry/node",
    label: "Node.js / Express",
    language: "typescript",
    note: "Import the instrumentation module before the rest of the application.",
    scope: "server",
    source: (dsn) => `import * as Sentry from "@sentry/node";
Sentry.init({
  dsn: ${quoted(dsn)},
});

// Express: call after routes are registered.
Sentry.setupExpressErrorHandler(app);`,
  },
  {
    id: "bun",
    install: "bun add @sentry/bun",
    label: "Bun",
    language: "typescript",
    note: "Initialize Sentry in the entry module before importing the rest of the application.",
    scope: "server",
    source: (dsn) => `import * as Sentry from "@sentry/bun";

Sentry.init({
  dsn: ${quoted(dsn)},
});`,
  },
  {
    id: "python",
    install: 'pip install "sentry-sdk[flask]"',
    label: "Python / Flask",
    language: "python",
    note: "For plain Python, omit FlaskIntegration and install sentry-sdk without the extra.",
    scope: "server",
    source: (dsn) => `import sentry_sdk
from sentry_sdk.integrations.flask import FlaskIntegration

sentry_sdk.init(
    dsn=${quoted(dsn)},
    integrations=[FlaskIntegration()],
)`,
  },
  {
    id: "go",
    install: "go get github.com/getsentry/sentry-go",
    label: "Go / net/http",
    language: "go",
    note: "Flush before short-lived processes exit; long-running servers send in the background.",
    scope: "server",
    source: (dsn) => `import (
    "log"
    "time"

    "github.com/getsentry/sentry-go"
)

if err := sentry.Init(sentry.ClientOptions{
    Dsn: ${quoted(dsn)},
}); err != nil {
    log.Fatal(err)
}
defer sentry.Flush(2 * time.Second)`,
  },
  {
    id: "rust",
    install: "cargo add sentry",
    label: "Rust",
    language: "rust",
    note: "Keep the guard alive for the lifetime of the process so queued events can be flushed.",
    scope: "server",
    source: (dsn) => `let _sentry_guard = sentry::init((
    ${quoted(dsn)},
    sentry::ClientOptions::default(),
));`,
  },
  {
    id: "java",
    install: "Maven: io.sentry:sentry",
    label: "Java",
    language: "java",
    note: "Use the matching Sentry framework integration for Spring, Log4j, or another stack.",
    scope: "server",
    source: (dsn) => `import io.sentry.Sentry;

Sentry.init(options ->
    options.setDsn(${quoted(dsn)})
);`,
  },
  {
    id: "dotnet",
    install: "dotnet add package Sentry",
    label: ".NET",
    language: "csharp",
    note: "ASP.NET Core applications can pass the same DSN to UseSentry.",
    scope: "server",
    source: (dsn) => `using Sentry;

using var sentry = SentrySdk.Init(options =>
    options.Dsn = ${quoted(dsn)}
);`,
  },
  {
    id: "php",
    install: "composer require sentry/sentry",
    label: "PHP",
    language: "php",
    note: "Initialize once during application bootstrap before handling requests.",
    scope: "server",
    source: (dsn) => `Sentry\\init([
    'dsn' => ${quoted(dsn)},
]);`,
  },
  {
    id: "ruby",
    install: "bundle add sentry-ruby",
    label: "Ruby",
    language: "ruby",
    note: "Rails applications can use sentry-rails with the same configuration block.",
    scope: "server",
    source: (dsn) => `require "sentry-ruby"

Sentry.init do |config|
  config.dsn = ${quoted(dsn)}
end`,
  },
];

export const RuntimeSetupGuide = ({
  app,
  notify,
}: {
  app: App;
  notify: (message: string) => void;
}) => {
  const [selectedId, setSelectedId] = useState(runtimeGuides[0]?.id ?? "");
  const [serverEndpoint, setServerEndpoint] = useState<"internal" | "public">(
    "internal"
  );
  const guide =
    runtimeGuides.find((candidate) => candidate.id === selectedId) ??
    runtimeGuides[0];
  if (!guide) {
    return null;
  }
  const effectiveServerEndpoint = app.publicDsn ? serverEndpoint : "internal";
  let dsn: string | undefined = app.internalDsn;
  if (guide.scope === "browser" || effectiveServerEndpoint === "public") {
    dsn = app.publicDsn;
  }
  const source = dsn
    ? guide.source(
        dsn,
        guide.scope === "browser" ? publicTunnelURL(app) : undefined
      )
    : "";
  const setup = `${guide.install}\n\n${source}`;

  return (
    <div className="max-w-4xl">
      <div
        aria-label="SDK runtime"
        className="flex flex-wrap border-y border-border"
        role="tablist"
      >
        {runtimeGuides.map((candidate) => (
          <button
            aria-selected={candidate.id === guide.id}
            className={cn(
              "border-r border-border px-3 py-2 text-[9px] text-muted-foreground hover:bg-muted/40 hover:text-foreground",
              candidate.id === guide.id && "bg-muted/60 text-foreground"
            )}
            key={candidate.id}
            onClick={() => setSelectedId(candidate.id)}
            role="tab"
            type="button"
          >
            {candidate.label}
          </button>
        ))}
      </div>

      {guide.scope === "server" && app.publicDsn ? (
        <div className="flex items-center gap-2 border-b border-border py-3">
          <span className="mr-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            Endpoint
          </span>
          <button
            className={cn(
              "border border-border px-2.5 py-1.5 text-[9px] text-muted-foreground",
              effectiveServerEndpoint === "internal" &&
                "border-foreground/50 bg-muted/60 text-foreground"
            )}
            onClick={() => setServerEndpoint("internal")}
            type="button"
          >
            Project network
          </button>
          <button
            className={cn(
              "border border-border px-2.5 py-1.5 text-[9px] text-muted-foreground",
              effectiveServerEndpoint === "public" &&
                "border-foreground/50 bg-muted/60 text-foreground"
            )}
            onClick={() => setServerEndpoint("public")}
            type="button"
          >
            Public internet
          </button>
        </div>
      ) : null}

      {dsn ? (
        <div className="border-b border-border">
          <div className="flex min-h-11 items-center justify-between gap-4 border-b border-border px-3">
            <code className="min-w-0 overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-foreground/75">
              {guide.install}
            </code>
            <CopyButton notify={notify} value={setup} />
          </div>
          <HighlightedSnippet
            className="border-0 bg-transparent px-3 py-4 pr-3"
            language={guide.language}
            value={source}
          />
        </div>
      ) : (
        <div className="border-b border-border py-5 text-[10px] leading-5 text-muted-foreground">
          Browser SDKs cannot reach the project-only hostname. Add a public
          Sentry domain above, then this guide will show the browser DSN.
        </div>
      )}
      <p className="mt-3 text-[9px] leading-4 text-muted-foreground">
        {guide.note}{" "}
        {guide.scope === "server" && effectiveServerEndpoint === "internal"
          ? "The internal DSN avoids public internet round trips."
          : null}
      </p>
    </div>
  );
};
