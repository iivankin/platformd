import {
  LoaderCircle,
  PackageSearch,
  RadioTower,
  RefreshCw,
  RotateCw,
  Search,
  Trash2,
} from "lucide-react";
import { useEffect, useState } from "react";
import type { ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  GitHubActionExampleDialog,
  portForwardActionExample,
} from "@/github-action-example-dialog";
import { HighlightedSnippet } from "@/snippet-code";

import { api } from "./api";
import { ErrorView, LoadingView } from "./common-ui";
import { CreateWebhookDialog } from "./create-webhook-dialog";
import { ArtifactsView } from "./data-views";
import { Modal } from "./dialog-frame";
import { errorMessage } from "./format";
import { RuntimeSetupGuide } from "./runtime-setup-guide";
import { CopyButton, SettingsSection } from "./settings-common";
import type {
  App,
  ListResponse,
  StoredDocument,
  TelemetryMetadata,
} from "./types";

const ArtifactInventory = ({ appId }: { appId: string }) => {
  const [query, setQuery] = useState("");
  const [revision, setRevision] = useState(0);
  const [result, setResult] = useState<{
    data?: ListResponse<StoredDocument>;
    error?: string;
    key: string;
  }>();
  const requestKey = `${appId}:${query}:${revision}`;

  useEffect(() => {
    let active = true;
    const timeout = setTimeout(
      () => {
        const load = async () => {
          try {
            const data = await api.artifacts(appId, query);
            if (active) {
              setResult({ data, key: requestKey });
            }
          } catch (error) {
            if (active) {
              setResult({
                error: errorMessage(error, "Unable to load artifacts"),
                key: requestKey,
              });
            }
          }
        };
        void load();
      },
      query ? 220 : 0
    );
    return () => {
      active = false;
      clearTimeout(timeout);
    };
  }, [appId, query, requestKey]);

  const current = result?.key === requestKey ? result : undefined;
  return (
    <section>
      <div className="flex min-h-12 items-center gap-3 border-b border-border px-3">
        <div className="relative min-w-0 flex-1">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="Search artifacts"
            className="h-7 max-w-sm pl-7 text-[10px]"
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search uploaded artifacts"
            type="search"
            value={query}
          />
        </div>
        {current?.data ? (
          <span className="text-[8px] text-muted-foreground">
            {current.data.total.toLocaleString()} artifacts
          </span>
        ) : null}
        <Button
          aria-label="Refresh artifacts"
          onClick={() => setRevision((value) => value + 1)}
          size="icon"
          variant="ghost"
        >
          <RefreshCw />
        </Button>
      </div>
      <div className="overflow-x-auto">
        {current?.error ? <ErrorView message={current.error} /> : null}
        {current?.data ? <ArtifactsView items={current.data.data} /> : null}
        {current ? null : <LoadingView label="Loading artifacts" />}
      </div>
    </section>
  );
};

export const ApplicationSettingsView = ({
  app,
  notify,
  refresh,
  settingsHeader,
  telemetry,
}: {
  app: App;
  notify: (message: string) => void;
  refresh: () => Promise<void>;
  settingsHeader?: ReactNode;
  telemetry: TelemetryMetadata;
}) => {
  const [uploadToken, setUploadToken] = useState("");
  const [rotatingToken, setRotatingToken] = useState(false);
  const [showArtifacts, setShowArtifacts] = useState(false);
  const [uploadTarget, setUploadTarget] = useState<"internal" | "public">(
    "internal"
  );
  const internalURL = new URL(app.internalDsn).origin;
  const internalUploadCommand = [
    "# sentry-cli requires a non-empty value; platformd ignores it on the private endpoint.",
    "SENTRY_AUTH_TOKEN=internal",
    `SENTRY_URL=${internalURL}`,
    `SENTRY_ORG=${telemetry.slug}`,
    `SENTRY_PROJECT=${app.id}`,
    "sentry-cli sourcemaps upload ./dist",
  ].join("\n");
  const publicUploadCommand = telemetry.publicUrl
    ? [
        `SENTRY_AUTH_TOKEN=${uploadToken || "<rotate-to-reveal>"}`,
        `SENTRY_URL=${telemetry.publicUrl}`,
        `SENTRY_ORG=${telemetry.slug}`,
        `SENTRY_PROJECT=${app.id}`,
        "sentry-cli sourcemaps upload ./dist",
      ].join("\n")
    : "";
  const tunnelExample = portForwardActionExample({
    kind: "errors",
    origin: telemetry.controlPlaneUrl,
    port: 9001,
    projectName: telemetry.projectName,
    resourceName: app.name,
    sentryProject: app.id,
  });

  const rotateUploadToken = async () => {
    setRotatingToken(true);
    try {
      const rotated = await api.rotateUploadToken(app.id);
      setUploadToken(rotated.authToken);
      notify("Upload token rotated; the previous token no longer works");
      await refresh();
    } catch (error) {
      notify(errorMessage(error, "Unable to rotate upload token"));
    } finally {
      setRotatingToken(false);
    }
  };

  const deleteWebhook = async (webhookId: string) => {
    try {
      await api.deleteWebhook(app.id, webhookId);
      notify("Webhook removed");
      await refresh();
    } catch (error) {
      notify(errorMessage(error, "Unable to remove webhook"));
    }
  };

  return (
    <div>
      {settingsHeader}
      <SettingsSection
        copy="Use this service's DSN with an official Sentry SDK. Choose a runtime for a minimal setup that sends errors to platformd."
        title="Connect an SDK"
      >
        <RuntimeSetupGuide app={app} notify={notify} />
      </SettingsSection>

      <SettingsSection
        action={
          <Button onClick={() => setShowArtifacts(true)} variant="outline">
            <PackageSearch />
            Browse artifacts
          </Button>
        }
        copy="Use the private endpoint from project workloads and service-scoped CI tunnels. The public endpoint uses a rotated upload token."
        title="Artifact uploads"
      >
        <div className="mb-4 flex max-w-3xl border-b border-border">
          {(["internal", "public"] as const).map((target) => (
            <button
              className={`relative h-9 px-3 text-[9px] tracking-[0.1em] uppercase after:absolute after:right-3 after:bottom-0 after:left-3 after:h-px ${
                uploadTarget === target
                  ? "text-foreground after:bg-foreground"
                  : "text-muted-foreground after:bg-transparent hover:text-foreground"
              }`}
              key={target}
              onClick={() => setUploadTarget(target)}
              type="button"
            >
              {target === "internal" ? "Project network" : "Public internet"}
            </button>
          ))}
        </div>
        {uploadTarget === "internal" ? (
          <div className="max-w-3xl">
            <div className="relative border-y border-border bg-muted/20">
              <HighlightedSnippet
                className="border-0 bg-transparent p-4 pr-24"
                language="bash"
                value={internalUploadCommand}
              />
              <div className="absolute top-2 right-2">
                <CopyButton notify={notify} value={internalUploadCommand} />
              </div>
            </div>
            <p className="mt-3 text-[9px] leading-4 text-muted-foreground">
              No upload credential is checked on the project network. The
              literal <code>internal</code> only satisfies sentry-cli&apos;s
              local non-empty-token validation.
            </p>
            <div className="mt-4 flex items-center justify-between gap-4 border-t border-border pt-4 max-sm:items-start">
              <p className="max-w-xl text-[9px] leading-4 text-muted-foreground">
                GitHub OIDC authorizes the short-lived service tunnel. No Sentry
                upload secret is needed in the workflow.
              </p>
              <GitHubActionExampleDialog
                description="Open the private errors endpoint for this service and upload source maps without a Sentry credential."
                example={tunnelExample}
                steps={[
                  "Allow the repository and workflow in this service's port-forward settings.",
                  "The action authorizes with GitHub OIDC and opens a service-scoped tunnel.",
                  "sentry-cli uses the exported localhost URL and the non-secret literal internal.",
                ]}
                title="Upload artifacts from GitHub Actions"
                triggerLabel="CI setup"
              />
            </div>
          </div>
        ) : (
          <div className="max-w-3xl">
            {publicUploadCommand ? (
              <div className="relative border-y border-border bg-muted/20">
                <HighlightedSnippet
                  className="border-0 bg-transparent p-4 pr-24"
                  language="bash"
                  value={publicUploadCommand}
                />
                {uploadToken ? (
                  <div className="absolute top-2 right-2">
                    <CopyButton
                      key={uploadToken}
                      notify={notify}
                      value={publicUploadCommand}
                    />
                  </div>
                ) : null}
              </div>
            ) : (
              <p className="border-y border-border py-4 text-[10px] leading-5 text-muted-foreground">
                Add a public Sentry domain before uploading from the internet.
              </p>
            )}
            <div className="mt-3 flex items-start gap-3 max-sm:flex-col">
              <Button
                disabled={rotatingToken || !publicUploadCommand}
                onClick={() => void rotateUploadToken()}
                variant="outline"
              >
                {rotatingToken ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <RotateCw />
                )}
                {rotatingToken ? "Rotating…" : "Rotate and reveal token"}
              </Button>
              <p className="pt-1 text-[10px] leading-4 text-muted-foreground">
                {uploadToken
                  ? "Shown once. Save SENTRY_AUTH_TOKEN now; leaving this service hides it."
                  : "Public uploads require this per-service token; only its hash is stored."}
              </p>
            </div>
          </div>
        )}
        <Modal
          className="max-w-5xl"
          description="Search source maps, debug files, and assembled release artifacts uploaded for this service."
          onOpenChange={setShowArtifacts}
          open={showArtifacts}
          title="Uploaded artifacts"
        >
          <div className="max-h-[70vh] overflow-y-auto">
            <ArtifactInventory appId={app.id} />
          </div>
        </Modal>
      </SettingsSection>

      <SettingsSection
        copy="This service has its own endpoint list, event filter, signing secret, and enabled state."
        title="Service webhooks"
      >
        <CreateWebhookDialog onCreated={refresh} serviceId={app.id} />
        <div className="mt-4 max-w-4xl divide-y divide-border border-y border-border">
          {app.webhooks.length === 0 ? (
            <div className="flex items-center gap-2 py-4 text-[10px] text-muted-foreground">
              <RadioTower className="size-3.5" /> No webhooks
            </div>
          ) : (
            app.webhooks.map((webhook) => (
              <div
                className="grid grid-cols-[minmax(180px,1fr)_minmax(220px,1fr)_auto] items-center gap-3 py-3 max-md:grid-cols-[minmax(0,1fr)_auto]"
                key={webhook.id}
              >
                <code className="overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-foreground/80">
                  {webhook.url}
                </code>
                <span className="overflow-hidden text-[9px] text-ellipsis whitespace-nowrap text-muted-foreground max-md:hidden">
                  {webhook.events.join(" · ")}
                </span>
                <Button
                  aria-label={`Delete ${webhook.url}`}
                  onClick={() => void deleteWebhook(webhook.id)}
                  size="icon"
                  variant="ghost"
                >
                  <Trash2 />
                </Button>
              </div>
            ))
          )}
        </div>
      </SettingsSection>
    </div>
  );
};
