import { CheckCircle2, GitFork, LockKeyhole } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  configureGitHubApp,
  fetchGitHubAppSettings,
  fetchInstallationSettings,
} from "@/api";
import type { GitHubAppSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { GitHubAppSetupGuide } from "@/github-app-setup-guide";
import { createGitHubWebhookSecret } from "@/github-webhook-secret";

const submitButtonLabel = (saving: boolean, configured: boolean) => {
  if (saving) {
    return "Verifying…";
  }
  return configured ? "Verify and replace" : "Verify and save";
};

export const SettingsGitHubPage = () => {
  const [settings, setSettings] = useState<GitHubAppSettings>();
  const [appID, setAppID] = useState("");
  const [privateKey, setPrivateKey] = useState("");
  const [webhookSecret, setWebhookSecret] = useState("");
  const [automationHostname, setAutomationHostname] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [loaded, installation] = await Promise.all([
          fetchGitHubAppSettings(controller.signal),
          fetchInstallationSettings(controller.signal),
        ]);
        setSettings(loaded);
        setAutomationHostname(installation.automationHostname);
        if (!loaded.configured) {
          setWebhookSecret((current) => current || createGitHubWebhookSecret());
        }
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
    setSaved(false);
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
      setSaved(true);
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
  const webhookURL = automationHostname
    ? `https://${automationHostname}${settings?.webhookPath ?? "/api/v1/integrations/github/webhook"}`
    : undefined;

  return (
    <div className="grid gap-4 p-5">
      <GitHubAppSetupGuide
        appSlug={settings?.appSlug || undefined}
        homepageURL={homepageURL}
        webhookSecret={webhookSecret || undefined}
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
          {settings?.configured ? (
            <span className="ml-auto flex items-center gap-1.5 text-[9px] text-emerald-600 dark:text-emerald-400">
              <CheckCircle2 className="size-3.5" />
              Configured · {settings.appSlug}
            </span>
          ) : (
            <span className="ml-auto text-[9px] text-muted-foreground">
              Not configured
            </span>
          )}
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
              onChange={(event) => {
                setAppID(event.target.value);
                setSaved(false);
              }}
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
              onChange={(event) => {
                setPrivateKey(event.target.value);
                setSaved(false);
              }}
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
            <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
              <div className="relative">
                <Input
                  className="pr-9"
                  id="github-webhook-secret"
                  minLength={16}
                  onChange={(event) => {
                    setWebhookSecret(event.target.value);
                    setSaved(false);
                  }}
                  placeholder={
                    settings?.configured
                      ? "Generate a new secret to rotate the existing one"
                      : undefined
                  }
                  required
                  type="password"
                  value={webhookSecret}
                />
                <LockKeyhole className="absolute top-1/2 right-3 size-3 -translate-y-1/2 text-muted-foreground" />
              </div>
              <Button
                onClick={() => {
                  setWebhookSecret(createGitHubWebhookSecret());
                  setSaved(false);
                }}
                type="button"
                variant="outline"
              >
                {webhookSecret ? "Regenerate" : "Generate secret"}
              </Button>
            </div>
            <span className="leading-4">
              Copy the generated value into the GitHub App before saving. The
              saved secret is write-only.
            </span>
          </label>
          {error ? (
            <p className="text-[10px] text-destructive">{error}</p>
          ) : null}
          <div className="flex flex-wrap items-center gap-3 border-t border-border pt-4">
            {settings?.configured ? (
              <p className="flex items-center gap-1.5 text-[10px] text-emerald-600 dark:text-emerald-400">
                <CheckCircle2 className="size-3.5" />
                {saved
                  ? "GitHub App verified and saved"
                  : `GitHub App configured as ${settings.appSlug}`}
              </p>
            ) : null}
            <Button className="ml-auto" disabled={saving} type="submit">
              {submitButtonLabel(saving, settings?.configured === true)}
            </Button>
          </div>
        </form>
      </SectionCard>
    </div>
  );
};
