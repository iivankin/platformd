import { ExternalLink, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  fetchInstallationSettings,
  setCloudflareAccessConfiguration,
} from "@/api";
import type { InstallationSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { SettingsError } from "@/settings-error";

const ZERO_TRUST_URL = "https://one.dash.cloudflare.com/";

const errorText = (error: unknown) =>
  error instanceof Error
    ? error.message
    : "Unable to update Cloudflare Access settings";

export const CloudflareAccessSettings = () => {
  const [settings, setSettings] = useState<InstallationSettings>();
  const [teamDomain, setTeamDomain] = useState("");
  const [audience, setAudience] = useState("");
  const [saving, setSaving] = useState(false);
  const [restarting, setRestarting] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchInstallationSettings(controller.signal);
        setSettings(loaded);
        setTeamDomain(loaded.accessTeamDomain);
        setAudience(loaded.accessAudience);
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
    try {
      const updated = await setCloudflareAccessConfiguration({
        audience: audience.trim(),
        teamDomain: teamDomain.trim(),
      });
      setSettings(updated);
      setTeamDomain(updated.accessTeamDomain);
      setAudience(updated.accessAudience);
      setRestarting(true);
    } catch (saveError) {
      setError(errorText(saveError));
    } finally {
      setSaving(false);
    }
  };

  const unchanged =
    teamDomain.trim().toLowerCase() === (settings?.accessTeamDomain ?? "") &&
    audience.trim() === (settings?.accessAudience ?? "");

  return (
    <>
      <SettingsError message={error} />
      {restarting ? (
        <SectionCard className="grid gap-3 border-l-2 border-l-emerald-500 px-5 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
          <div>
            <p className="text-xs font-medium">Access verifier is restarting</p>
            <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
              Reload when platformd is ready and authenticate through the
              configured Cloudflare Access application.
            </p>
          </div>
          <a
            className="inline-flex h-7 w-fit items-center bg-primary px-2 text-xs font-medium text-primary-foreground hover:bg-primary/85"
            href="/settings/cloudflare"
          >
            Reload settings
          </a>
        </SectionCard>
      ) : null}
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h2 className="flex items-center gap-2 text-xs font-medium">
            <ShieldCheck className="size-4 text-muted-foreground" /> Cloudflare
            Access
          </h2>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Values supplied during <code>platformd init</code> and used to
            verify every admin session.
          </p>
          <a
            className="mt-3 inline-flex items-center gap-1 text-[9px] text-foreground underline underline-offset-4"
            href={ZERO_TRUST_URL}
            rel="noreferrer"
            target="_blank"
          >
            Open Zero Trust <ExternalLink className="size-3" />
          </a>
        </div>
        <form
          className="grid gap-4 border-t border-border p-5 lg:border-t-0 lg:border-l"
          onSubmit={save}
        >
          <div className="grid gap-3 sm:grid-cols-2">
            <label
              className="grid gap-1.5 text-[9px] text-muted-foreground"
              htmlFor="cloudflare-access-team-domain"
            >
              Team domain
              <Input
                autoCapitalize="none"
                autoComplete="off"
                disabled={saving || restarting}
                id="cloudflare-access-team-domain"
                maxLength={253}
                onChange={(event) => setTeamDomain(event.target.value)}
                placeholder="team.cloudflareaccess.com"
                required
                spellCheck={false}
                value={teamDomain}
              />
            </label>
            <label
              className="grid gap-1.5 text-[9px] text-muted-foreground"
              htmlFor="cloudflare-access-audience"
            >
              Application AUD
              <Input
                autoCapitalize="none"
                autoComplete="off"
                disabled={saving || restarting}
                id="cloudflare-access-audience"
                maxLength={512}
                onChange={(event) => setAudience(event.target.value)}
                placeholder="Access application audience tag"
                required
                spellCheck={false}
                value={audience}
              />
            </label>
          </div>
          <div className="flex flex-wrap items-center gap-3 border-t border-border pt-4">
            <p className="max-w-xl text-[9px] leading-4 text-amber-600 dark:text-amber-400">
              Confirm these values in Cloudflare first. Saving restarts
              platformd and ends the current Access session.
            </p>
            <Button
              className="ml-auto"
              disabled={
                saving ||
                restarting ||
                unchanged ||
                !teamDomain.trim() ||
                !audience.trim()
              }
              type="submit"
            >
              {saving ? "Restarting…" : "Save and restart"}
            </Button>
          </div>
        </form>
      </SectionCard>
    </>
  );
};
