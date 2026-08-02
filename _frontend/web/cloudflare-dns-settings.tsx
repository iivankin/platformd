import { CheckCircle2, Cloud, ExternalLink, LockKeyhole } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { configureCloudflareDNS, fetchCloudflareDNSSettings } from "@/api";
import type { CloudflareDNSSettings as DNSSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { SettingsError } from "@/settings-error";

const TOKEN_URL = "https://dash.cloudflare.com/profile/api-tokens";

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : "Unable to configure Cloudflare DNS";

export const CloudflareDNSSettings = () => {
  const [settings, setSettings] = useState<DNSSettings>();
  const [apiToken, setAPIToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setSettings(await fetchCloudflareDNSSettings(controller.signal));
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSaving(true);
    setError(undefined);
    setSaved(false);
    try {
      setSettings(await configureCloudflareDNS({ apiToken }));
      setAPIToken("");
      setSaved(true);
    } catch (saveError) {
      setError(errorText(saveError));
    } finally {
      setSaving(false);
    }
  };

  let submitLabel = "Verify and save";
  if (saving) {
    submitLabel = "Verifying…";
  } else if (settings?.configured) {
    submitLabel = "Verify and replace";
  }

  return (
    <>
      <SettingsError message={error} />
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h2 className="flex items-center gap-2 text-xs font-medium">
            <Cloud className="size-4 text-muted-foreground" /> Cloudflare DNS
          </h2>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Used for service and PR preview DNS records, plus before-deploy
            cache purges.
          </p>
          <p className="mt-3 text-[9px] text-muted-foreground">
            {settings?.configured ? "Configured" : "Not configured"}
          </p>
        </div>
        <form
          className="grid gap-4 border-t border-border p-5 lg:border-t-0 lg:border-l"
          onSubmit={save}
        >
          <div className="grid gap-2 text-[9px] leading-4 text-muted-foreground">
            <p>
              Create a scoped API Token with Zone · Zone · Read, Zone · DNS ·
              Edit, and Zone · Cache Purge · Purge for the zones used by
              services.
            </p>
            <a
              className="inline-flex w-fit items-center gap-1 text-foreground underline underline-offset-4"
              href={TOKEN_URL}
              rel="noreferrer"
              target="_blank"
            >
              Create Cloudflare API Token <ExternalLink className="size-3" />
            </a>
          </div>
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="cloudflare-api-token"
          >
            API Token
            <div className="relative">
              <Input
                className="pr-9"
                id="cloudflare-api-token"
                minLength={20}
                onChange={(event) => {
                  setAPIToken(event.target.value);
                  setSaved(false);
                }}
                placeholder={
                  settings?.configured
                    ? "Token saved — enter a new token to replace it"
                    : "Paste API token"
                }
                required
                type="password"
                value={apiToken}
              />
              <LockKeyhole className="absolute top-1/2 right-3 size-3 -translate-y-1/2 text-muted-foreground" />
            </div>
          </label>
          <div className="flex flex-wrap items-center gap-3 border-t border-border pt-4">
            {settings?.configured ? (
              <p className="flex items-center gap-1.5 text-[10px] text-emerald-600 dark:text-emerald-400">
                <CheckCircle2 className="size-3.5" />
                {saved ? "Token verified and saved" : "Token configured"}
              </p>
            ) : null}
            <Button
              className="ml-auto"
              disabled={saving || apiToken.trim().length < 20}
              type="submit"
            >
              {submitLabel}
            </Button>
          </div>
        </form>
      </SectionCard>
    </>
  );
};
