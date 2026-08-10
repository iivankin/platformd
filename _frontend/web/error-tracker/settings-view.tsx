import { LoaderCircle, RadioTower, RotateCw, Trash2 } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";

import { api } from "./api";
import { CreateWebhookDialog } from "./create-webhook-dialog";
import { errorMessage } from "./format";
import { CopyButton, SettingsSection } from "./settings-common";
import type { App, Tracker } from "./types";

export const ApplicationSettingsView = ({
  app,
  notify,
  refresh,
  tracker,
}: {
  app: App;
  notify: (message: string) => void;
  refresh: () => Promise<void>;
  tracker: Tracker;
}) => {
  const [uploadToken, setUploadToken] = useState("");
  const [rotatingToken, setRotatingToken] = useState(false);
  const uploadCommand = [
    `SENTRY_AUTH_TOKEN=${uploadToken || "<rotate-to-reveal>"}`,
    `SENTRY_URL=${tracker.publicUrl}`,
    `SENTRY_ORG=${tracker.slug}`,
    `SENTRY_PROJECT=${app.slug}`,
    "sentry-cli sourcemaps upload ./dist",
  ].join("\n");

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
      <SettingsSection
        copy="Each application has one sentry-cli upload token. Rotating it immediately invalidates the previous value."
        title="Artifact uploads"
      >
        <div className="relative max-w-3xl border border-border bg-muted/20">
          <pre className="overflow-x-auto p-4 pr-24 text-[10px] leading-5 text-foreground/75">
            {uploadCommand}
          </pre>
          {uploadToken ? (
            <div className="absolute top-2 right-2">
              <CopyButton
                key={uploadToken}
                notify={notify}
                value={uploadCommand}
              />
            </div>
          ) : null}
        </div>
        <div className="mt-3 flex max-w-3xl items-start gap-3 max-sm:flex-col">
          <Button
            disabled={rotatingToken}
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
              ? "Shown once. Update SENTRY_AUTH_TOKEN now; leaving this application hides the secret."
              : "The current secret cannot be recovered because only its hash is stored."}
          </p>
        </div>
      </SettingsSection>

      <SettingsSection
        copy="This application has its own endpoint list, event filter, signing secret, and enabled state."
        title="Application webhooks"
      >
        <CreateWebhookDialog appId={app.id} onCreated={refresh} />
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
