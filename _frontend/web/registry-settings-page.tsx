import { Globe2 } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { Link } from "react-router";

import { fetchRegistrySettings, setRegistryHostname } from "@/api";
import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { Button } from "@/components/ui/button";
import { FormCard, SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export const RegistrySettingsPage = () => {
  const [hostname, setHostname] = useState("");
  const [hostnameInput, setHostnameInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const settings = await fetchRegistrySettings(controller.signal);
        setHostname(settings.hostname);
        setHostnameInput(settings.hostname);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load Registry settings"));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const saveHostname = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const updated = await setRegistryHostname(hostnameInput.trim());
      setHostname(updated.hostname);
      setHostnameInput(updated.hostname);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to update Registry domain"));
    } finally {
      setBusy(false);
    }
  };

  return (
    <PageStack>
      <SectionCard className="flex items-center gap-4 px-5 py-5">
        <span className="grid size-9 place-items-center bg-muted">
          <Globe2 className="size-4" />
        </span>
        <div>
          <h3 className="text-xs font-medium">Public registry</h3>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Domain used in image references for push and pull.
          </p>
        </div>
      </SectionCard>

      <FormCard
        className="grid lg:grid-cols-[220px_minmax(16rem,1fr)_auto] lg:items-center"
        onSubmit={saveHostname}
      >
        <label
          className="px-5 py-4 text-[10px] font-medium"
          htmlFor="registry-hostname"
        >
          Registry domain
        </label>
        <div className="border-y border-border px-4 py-3 lg:border-x lg:border-y-0">
          <CertificateHostnameCombobox
            ariaLabel="Registry domain"
            disabled={busy}
            id="registry-hostname"
            onChange={setHostnameInput}
            placeholder="registry.example.com"
            value={hostnameInput}
          />
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            DNS must point to this server. An installed Origin certificate must
            cover the domain.{" "}
            <Link
              className="text-foreground underline underline-offset-2"
              to="/settings/certificates"
            >
              Manage certificates
            </Link>
          </p>
        </div>
        <div className="flex gap-2 px-4 py-3">
          <Button
            disabled={busy || hostnameInput === hostname}
            size="sm"
            type="submit"
          >
            {busy ? "Saving…" : "Save"}
          </Button>
          {hostname ? (
            <Button
              disabled={busy}
              onClick={() => setHostnameInput("")}
              size="sm"
              type="button"
              variant="ghost"
            >
              Clear
            </Button>
          ) : null}
        </div>
      </FormCard>

      {error ? (
        <SectionCard className="bg-destructive/5 px-5 py-3 text-[10px] text-destructive ring-destructive/30">
          {error}
        </SectionCard>
      ) : null}
    </PageStack>
  );
};
