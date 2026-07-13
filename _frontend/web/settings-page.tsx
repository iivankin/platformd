import { Globe2, KeyRound, Plus, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  addOriginCertificate,
  deleteOriginCertificate,
  fetchInstallationSettings,
  replaceOriginCertificate,
  setAutomationHostname as updateAutomationHostname,
} from "@/api";
import type { InstallationSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { SettingsCertificateRow } from "@/settings-certificate-row";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const certificateSubmitLabel = (saving: boolean, replacingID?: string) => {
  if (saving) {
    return "Saving…";
  }
  return replacingID ? "Replace certificate" : "Add certificate";
};

const SettingsError = ({ message }: { message?: string }) => {
  if (!message) {
    return null;
  }
  return (
    <section className="flex items-center gap-2 border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-xs text-destructive">
      <X className="size-3.5" /> {message}
    </section>
  );
};

export const SettingsPage = () => {
  const [settings, setSettings] = useState<InstallationSettings>();
  const [automationHostname, setAutomationHostname] = useState("");
  const [certificatePEM, setCertificatePEM] = useState("");
  const [privateKeyPEM, setPrivateKeyPEM] = useState("");
  const [replacingID, setReplacingID] = useState<string>();
  const [deletingID, setDeletingID] = useState<string>();
  const [busy, setBusy] = useState("");
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
    setBusy("hostname");
    setError(undefined);
    try {
      const updated = await updateAutomationHostname(automationHostname.trim());
      setSettings(updated);
      setAutomationHostname(updated.automationHostname);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to update automation hostname"));
    } finally {
      setBusy("");
    }
  };

  const saveCertificate = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy("certificate");
    setError(undefined);
    try {
      const input = {
        certificatePem: certificatePEM,
        privateKeyPem: privateKeyPEM,
      };
      const updated = replacingID
        ? await replaceOriginCertificate(replacingID, input)
        : await addOriginCertificate(input);
      setSettings(updated);
      setCertificatePEM("");
      setPrivateKeyPEM("");
      setReplacingID(undefined);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to save Origin certificate"));
    } finally {
      setBusy("");
    }
  };

  const removeCertificate = async (certificateID: string) => {
    setBusy(`delete:${certificateID}`);
    setError(undefined);
    try {
      setSettings(await deleteOriginCertificate(certificateID));
      setDeletingID(undefined);
    } catch (deleteError) {
      setError(errorText(deleteError, "Unable to delete Origin certificate"));
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="enter-row min-h-full">
      <section className="border-b border-border px-5 py-4">
        <p className="text-xs font-medium">Installation</p>
        <p className="mt-1 text-[10px] text-muted-foreground">
          Live control-plane routing and Cloudflare identity settings.
        </p>
      </section>

      <SettingsError message={error} />

      <section className="grid border-b border-border md:grid-cols-2">
        {[
          ["Admin hostname", settings?.adminHostname ?? "—"],
          ["Installation ID", settings?.installationId ?? "—"],
          ["Access team", settings?.accessTeamDomain ?? "—"],
          ["Access audience", settings?.accessAudience ?? "—"],
        ].map(([label, value]) => (
          <div
            className="border-b border-border px-5 py-4 odd:md:border-r"
            key={label}
          >
            <p className="text-[10px] tracking-[0.12em] text-muted-foreground uppercase">
              {label}
            </p>
            <p className="mt-2 font-mono text-[10px] break-all">{value}</p>
          </div>
        ))}
      </section>

      <form
        className="grid border-b border-border lg:grid-cols-[220px_minmax(16rem,1fr)_auto] lg:items-center"
        onSubmit={saveAutomationHostname}
      >
        <div className="flex items-center gap-2 px-5 py-4 text-xs font-medium">
          <Globe2 className="size-4 text-muted-foreground" /> REST API & MCP
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
              Boolean(busy) ||
              automationHostname === (settings?.automationHostname ?? "")
            }
            size="sm"
            type="submit"
          >
            {busy === "hostname" ? "Saving…" : "Save"}
          </Button>
          {settings?.automationHostname ? (
            <Button
              disabled={Boolean(busy)}
              onClick={() => setAutomationHostname("")}
              size="sm"
              type="button"
              variant="ghost"
            >
              Disable
            </Button>
          ) : null}
        </div>
      </form>

      <section className="flex items-center justify-between border-b border-border px-5 py-4">
        <div>
          <p className="flex items-center gap-2 text-xs font-medium">
            <KeyRound className="size-4 text-muted-foreground" /> Cloudflare
            Origin certificates
          </p>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Keys are encrypted before SQLite commit and are never returned by
            the API.
          </p>
        </div>
        <Button
          onClick={() => {
            setReplacingID(undefined);
            setCertificatePEM("");
            setPrivateKeyPEM("");
          }}
          size="sm"
          variant="outline"
        >
          <Plus /> Add
        </Button>
      </section>

      {settings?.certificates.map((certificate) => (
        <SettingsCertificateRow
          busy={Boolean(busy)}
          certificate={certificate}
          deleting={deletingID === certificate.id}
          key={certificate.id}
          onCancelDelete={() => setDeletingID(undefined)}
          onDelete={() => void removeCertificate(certificate.id)}
          onReplace={() => {
            setReplacingID(certificate.id);
            setCertificatePEM("");
            setPrivateKeyPEM("");
          }}
          onStartDelete={() => setDeletingID(certificate.id)}
        />
      ))}

      <form className="border-b border-border" onSubmit={saveCertificate}>
        <div className="flex items-center justify-between border-b border-border bg-muted/20 px-5 py-3">
          <p className="text-xs font-medium">
            {replacingID ? `Replace ${replacingID}` : "Add Origin certificate"}
          </p>
          {replacingID ? (
            <Button
              onClick={() => {
                setReplacingID(undefined);
                setCertificatePEM("");
                setPrivateKeyPEM("");
              }}
              size="sm"
              type="button"
              variant="ghost"
            >
              Cancel
            </Button>
          ) : null}
        </div>
        <div className="grid lg:grid-cols-2">
          <label className="px-5 py-4 lg:border-r lg:border-border">
            <span className="text-[10px] font-medium">
              Certificate chain PEM
            </span>
            <textarea
              className="mt-2 min-h-44 w-full resize-y border border-input bg-transparent p-3 font-mono text-[10px] outline-none focus:border-ring"
              onChange={(event) => setCertificatePEM(event.target.value)}
              required
              value={certificatePEM}
            />
          </label>
          <label className="border-t border-border px-5 py-4 lg:border-t-0">
            <span className="text-[10px] font-medium">Private key PEM</span>
            <textarea
              autoComplete="off"
              className="mt-2 min-h-44 w-full resize-y border border-input bg-transparent p-3 font-mono text-[10px] outline-none focus:border-ring"
              onChange={(event) => setPrivateKeyPEM(event.target.value)}
              required
              value={privateKeyPEM}
            />
          </label>
        </div>
        <div className="flex justify-end border-t border-border px-5 py-3">
          <Button
            disabled={Boolean(busy) || !certificatePEM || !privateKeyPEM}
            size="sm"
            type="submit"
          >
            {certificateSubmitLabel(busy === "certificate", replacingID)}
          </Button>
        </div>
      </form>
    </div>
  );
};
