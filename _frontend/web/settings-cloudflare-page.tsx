import { Cloud, ExternalLink, LockKeyhole } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { configureCloudflareDNS, fetchCloudflareDNSSettings } from "@/api";
import type { CloudflareDNSSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

const TOKEN_URL = "https://dash.cloudflare.com/profile/api-tokens";

export const SettingsCloudflarePage = () => {
  const [settings, setSettings] = useState<CloudflareDNSSettings>();
  const [apiToken, setAPIToken] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();

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
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load Cloudflare settings"
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
      setSettings(await configureCloudflareDNS({ apiToken }));
      setAPIToken("");
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to configure Cloudflare DNS"
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="grid gap-4 p-5">
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h2 className="flex items-center gap-2 text-xs font-medium">
            <Cloud className="size-4 text-muted-foreground" /> Cloudflare DNS
          </h2>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Used only to create and remove PR preview hostnames.
          </p>
          <p className="mt-3 text-[9px] text-muted-foreground">
            {settings?.configured ? "Configured" : "Not configured"}
          </p>
        </div>
        <form
          className="grid gap-4 border-t border-border p-5 lg:border-t-0 lg:border-l"
          onSubmit={submit}
        >
          <div className="grid gap-2 text-[9px] leading-4 text-muted-foreground">
            <p>
              Create a scoped API Token with Zone · Zone · Read and Zone · DNS ·
              Edit for the zones that host preview domains.
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
                onChange={(event) => setAPIToken(event.target.value)}
                required
                type="password"
                value={apiToken}
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
