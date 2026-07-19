import { GitFork, LockKeyhole } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { configureGitHubApp, fetchGitHubAppSettings } from "@/api";
import type { GitHubAppSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { GitHubAppSetupGuide } from "@/github-app-setup-guide";

export const SettingsGitHubPage = () => {
  const [settings, setSettings] = useState<GitHubAppSettings>();
  const [appID, setAppID] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [webhookSecret, setWebhookSecret] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchGitHubAppSettings(controller.signal);
        setSettings(loaded);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load GitHub settings"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSaving(true);
    setError(undefined);
    try {
      const configuredAppID = Number(appID || settings?.appId || 0);
      const updated = await configureGitHubApp({
        appId: configuredAppID,
        privateKeyPem: privateKey,
        webhookSecret,
      });
      setSettings(updated);
      setAppID("");
      setPrivateKey("");
      setWebhookSecret("");
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to configure GitHub App"
      );
    } finally {
      setSaving(false);
    }
  };

  const homepageURL = globalThis.location.origin;
  const webhookURL = `${homepageURL}${
    settings?.webhookPath ?? "/api/v1/integrations/github/webhook"
  }`;

  return (
    <div className="grid gap-4 p-5">
      <GitHubAppSetupGuide
        appSlug={settings?.appSlug || undefined}
        homepageURL={homepageURL}
        webhookURL={webhookURL}
      />
      <SectionCard>
        <div className="flex items-start gap-3 border-b border-border px-5 py-4">
          <GitFork className="mt-0.5 size-4 text-muted-foreground" />
          <div>
            <h2 className="text-xs font-medium">GitHub App</h2>
            <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
              Builds use installation tokens. The private key and webhook secret
              are encrypted with the platformd master key.
            </p>
          </div>
          <span className="ml-auto text-[9px] text-muted-foreground">
            {settings?.configured ? settings.appSlug : "Not configured"}
          </span>
        </div>
        <form className="grid gap-4 p-5" onSubmit={submit}>
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="github-app-id"
          >
            App ID
            <Input
              id="github-app-id"
              min={1}
              onChange={(event) => setAppID(event.target.value)}
              placeholder={settings?.appId ? String(settings.appId) : "1234567"}
              required={!settings?.appId}
              type="number"
              value={appID}
            />
          </label>
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="github-private-key"
          >
            Private key PEM
            <textarea
              className="min-h-40 resize-y border border-input bg-background px-2.5 py-2 font-mono text-[10px] leading-4 outline-none placeholder:text-muted-foreground/70 focus-visible:border-ring focus-visible:ring-1 focus-visible:ring-ring"
              id="github-private-key"
              onChange={(event) => setPrivateKey(event.target.value)}
              placeholder="-----BEGIN RSA PRIVATE KEY-----"
              required
              value={privateKey}
            />
          </label>
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="github-webhook-secret"
          >
            Webhook secret
            <div className="relative">
              <Input
                className="pr-9"
                id="github-webhook-secret"
                minLength={16}
                onChange={(event) => setWebhookSecret(event.target.value)}
                required
                type="password"
                value={webhookSecret}
              />
              <LockKeyhole className="absolute top-1/2 right-3 size-3 -translate-y-1/2 text-muted-foreground" />
            </div>
          </label>
          {error ? (
            <p className="text-[10px] text-destructive">{error}</p>
          ) : null}
          <div className="flex justify-end border-t border-border pt-4">
            <Button disabled={saving} type="submit">
              {saving ? "Verifying…" : "Verify and save"}
            </Button>
          </div>
        </form>
      </SectionCard>
    </div>
  );
};
