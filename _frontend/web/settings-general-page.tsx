import { ExternalLink, KeyRound, Route, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  fetchInstallationSettings,
  setAdminHostname as updateAdminHostname,
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
  const [adminHostname, setAdminHostname] = useState("");
  const [savingAdmin, setSavingAdmin] = useState(false);
  const [transitionHostname, setTransitionHostname] = useState<string>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchInstallationSettings(controller.signal);
        setSettings(loaded);
        setAdminHostname(loaded.adminHostname);
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

  const saveAdminHostname = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSavingAdmin(true);
    setError(undefined);
    try {
      const updated = await updateAdminHostname(adminHostname.trim());
      setSettings(updated);
      setAdminHostname(updated.adminHostname);
      setTransitionHostname(updated.adminHostname);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to update admin hostname"));
      setSavingAdmin(false);
    }
  };

  return (
    <PageStack>
      <SettingsError message={error} />

      {transitionHostname ? (
        <SectionCard className="grid gap-3 border-l-2 border-l-emerald-500 px-5 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
          <div>
            <p className="text-xs font-medium">Control plane is restarting</p>
            <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
              Continue at https://{transitionHostname} when the new address is
              ready.
            </p>
          </div>
          <a
            className="inline-flex h-7 w-fit items-center gap-1.5 bg-primary px-2 text-xs font-medium text-primary-foreground hover:bg-primary/85"
            href={`https://${transitionHostname}/settings/general`}
          >
            Open new address <ExternalLink className="size-3.5" />
          </a>
        </SectionCard>
      ) : null}

      <FormCard
        className="grid lg:grid-cols-[220px_minmax(16rem,1fr)_auto] lg:items-center"
        onSubmit={saveAdminHostname}
      >
        <div className="px-5 py-4">
          <p className="flex items-center gap-2 text-xs font-medium">
            <ShieldCheck className="size-4 text-muted-foreground" /> Admin
            address
          </p>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            Saving restarts platformd and disables the current address.
          </p>
        </div>
        <div className="border-y border-border px-4 py-3 lg:border-x lg:border-y-0">
          <CertificateHostnameCombobox
            ariaLabel="Admin hostname"
            disabled={savingAdmin || Boolean(transitionHostname)}
            onChange={setAdminHostname}
            placeholder="admin.example.com"
            value={adminHostname}
          />
        </div>
        <div className="px-4 py-3">
          <Button
            disabled={
              savingAdmin ||
              Boolean(transitionHostname) ||
              adminHostname.trim() === (settings?.adminHostname ?? "")
            }
            size="sm"
            type="submit"
          >
            {savingAdmin ? "Restarting…" : "Save and restart"}
          </Button>
        </div>
      </FormCard>

      <SectionCard className="grid lg:grid-cols-[220px_minmax(16rem,1fr)_minmax(18rem,1fr)] lg:items-stretch">
        <div className="px-5 py-4">
          <p className="flex items-center gap-2 text-xs font-medium">
            <Route className="size-4 text-muted-foreground" /> Public API path
          </p>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            Public traffic shares the admin hostname.
          </p>
        </div>
        <div className="flex items-center border-y border-border px-4 py-3 lg:border-x lg:border-y-0">
          <code className="w-full overflow-x-auto border border-border bg-background px-3 py-2 text-[10px] leading-5 select-all">
            {settings?.adminHostname
              ? `https://${settings.adminHostname}/public/*`
              : "/public/*"}
          </code>
        </div>
        <div className="flex gap-3 px-5 py-4">
          <KeyRound className="mt-0.5 size-4 shrink-0 text-amber-500" />
          <div>
            <p className="text-[10px] font-medium">
              Cloudflare Access: Bypass required
            </p>
            <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
              Bypass <code>/public/*</code> in Cloudflare Access. REST and MCP
              authenticate with a platformd API token. Webhooks and port
              forwarding use their own scoped credentials.
            </p>
          </div>
        </div>
      </SectionCard>
    </PageStack>
  );
};
