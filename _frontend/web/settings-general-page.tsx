import { Globe2, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  fetchInstallationSettings,
  setAutomationHostname as updateAutomationHostname,
} from "@/api";
import type { InstallationSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingsError } from "@/settings-error";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export const SettingsGeneralPage = () => {
  const [settings, setSettings] = useState<InstallationSettings>();
  const [automationHostname, setAutomationHostname] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchInstallationSettings(controller.signal);
        setSettings(loaded);
        setAutomationHostname(loaded.automationHostname);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            errorText(loadError, "Unable to load installation settings")
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const saveAutomationHostname = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSaving(true);
    setError(undefined);
    try {
      const updated = await updateAutomationHostname(automationHostname.trim());
      setSettings(updated);
      setAutomationHostname(updated.automationHostname);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to update automation hostname"));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div>
      <SettingsError message={error} />

      <section className="flex items-center gap-4 border-b border-border px-5 py-5">
        <span className="grid size-9 place-items-center bg-muted">
          <ShieldCheck className="size-4" />
        </span>
        <div className="min-w-0">
          <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
            Admin address
          </p>
          <p className="mt-1 truncate text-xs font-medium">
            {settings?.adminHostname ?? "Loading…"}
          </p>
        </div>
      </section>

      <form
        className="grid border-b border-border lg:grid-cols-[220px_minmax(16rem,1fr)_auto] lg:items-center"
        onSubmit={saveAutomationHostname}
      >
        <div className="px-5 py-4">
          <p className="flex items-center gap-2 text-xs font-medium">
            <Globe2 className="size-4 text-muted-foreground" /> Automation
            access
          </p>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            Optional address for tools and deployment systems.
          </p>
        </div>
        <div className="border-y border-border px-4 py-3 lg:border-x lg:border-y-0">
          <Input
            aria-label="Automation hostname"
            onChange={(event) => setAutomationHostname(event.target.value)}
            placeholder="api.example.com"
            value={automationHostname}
          />
        </div>
        <div className="flex gap-2 px-4 py-3">
          <Button
            disabled={
              saving ||
              automationHostname === (settings?.automationHostname ?? "")
            }
            size="sm"
            type="submit"
          >
            {saving ? "Saving…" : "Save"}
          </Button>
          {settings?.automationHostname ? (
            <Button
              disabled={saving}
              onClick={() => setAutomationHostname("")}
              size="sm"
              type="button"
              variant="ghost"
            >
              Clear
            </Button>
          ) : null}
        </div>
      </form>

      <details className="border-b border-border">
        <summary className="cursor-pointer px-5 py-3 text-[10px] text-muted-foreground hover:text-foreground">
          Advanced identity details
        </summary>
        <dl className="grid border-t border-border bg-muted/15 md:grid-cols-3">
          {[
            ["Installation ID", settings?.installationId ?? "—"],
            ["Access team", settings?.accessTeamDomain ?? "—"],
            ["Access audience", settings?.accessAudience ?? "—"],
          ].map(([label, value]) => (
            <div
              className="min-w-0 border-b border-border px-5 py-4 md:border-r md:border-b-0"
              key={label}
            >
              <dt className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                {label}
              </dt>
              <dd className="mt-2 truncate font-mono text-[9px]" title={value}>
                {value}
              </dd>
            </div>
          ))}
        </dl>
      </details>
    </div>
  );
};
