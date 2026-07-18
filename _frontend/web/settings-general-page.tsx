import { Globe2, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  fetchInstallationSettings,
  setAutomationHostname as updateAutomationHostname,
} from "@/api";
import type { InstallationSettings } from "@/api";
import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { Button } from "@/components/ui/button";
import { FormCard, SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
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
    <PageStack>
      <SettingsError message={error} />

      <SectionCard className="flex items-center gap-4 px-5 py-5">
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
      </SectionCard>

      <FormCard
        className="grid lg:grid-cols-[220px_minmax(16rem,1fr)_auto] lg:items-center"
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
          <CertificateHostnameCombobox
            ariaLabel="Automation hostname"
            disabled={saving}
            onChange={setAutomationHostname}
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
      </FormCard>
    </PageStack>
  );
};
