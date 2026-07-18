import { KeyRound, Plus } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  addOriginCertificate,
  deleteOriginCertificate,
  fetchInstallationSettings,
  replaceOriginCertificate,
} from "@/api";
import type { InstallationSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { FormCard, SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
import { SettingsCertificateRow } from "@/settings-certificate-row";
import { SettingsError } from "@/settings-error";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const submitLabel = (saving: boolean, replacing: boolean) => {
  if (saving) {
    return "Saving…";
  }
  return replacing ? "Replace certificate" : "Add certificate";
};

export const SettingsCertificatesPage = () => {
  const [settings, setSettings] = useState<InstallationSettings>();
  const [certificatePEM, setCertificatePEM] = useState("");
  const [privateKeyPEM, setPrivateKeyPEM] = useState("");
  const [editing, setEditing] = useState(false);
  const [replacingID, setReplacingID] = useState<string>();
  const [deletingID, setDeletingID] = useState<string>();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setSettings(await fetchInstallationSettings(controller.signal));
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load Origin certificates"));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const closeEditor = () => {
    setEditing(false);
    setReplacingID(undefined);
    setCertificatePEM("");
    setPrivateKeyPEM("");
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
      closeEditor();
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
    <PageStack>
      <SettingsError message={error} />

      <SectionCard className="flex items-center justify-between gap-4 px-5 py-4">
        <div>
          <p className="flex items-center gap-2 text-xs font-medium">
            <KeyRound className="size-4 text-muted-foreground" /> Origin
            certificates
          </p>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Certificates used for secure traffic to this installation.
          </p>
        </div>
        <Button
          disabled={Boolean(busy)}
          onClick={() => {
            setEditing(true);
            setReplacingID(undefined);
            setCertificatePEM("");
            setPrivateKeyPEM("");
          }}
          size="sm"
          variant="outline"
        >
          <Plus /> Add certificate
        </Button>
      </SectionCard>

      {settings?.certificates.length === 0 ? (
        <SectionCard className="px-5 py-10 text-center text-[10px] text-muted-foreground">
          No Origin certificates have been added.
        </SectionCard>
      ) : null}

      {settings?.certificates.map((certificate) => (
        <SettingsCertificateRow
          busy={Boolean(busy)}
          certificate={certificate}
          deleting={deletingID === certificate.id}
          key={certificate.id}
          onCancelDelete={() => setDeletingID(undefined)}
          onDelete={() => void removeCertificate(certificate.id)}
          onReplace={() => {
            setEditing(true);
            setReplacingID(certificate.id);
            setCertificatePEM("");
            setPrivateKeyPEM("");
          }}
          onStartDelete={() => setDeletingID(certificate.id)}
        />
      ))}

      {editing ? (
        <FormCard onSubmit={saveCertificate}>
          <div className="flex items-center justify-between border-b border-border bg-muted/20 px-5 py-3">
            <p className="text-xs font-medium">
              {replacingID ? "Replace certificate" : "Add certificate"}
            </p>
            <Button
              onClick={closeEditor}
              size="sm"
              type="button"
              variant="ghost"
            >
              Cancel
            </Button>
          </div>
          <div className="grid lg:grid-cols-2">
            <label className="px-5 py-4 lg:border-r lg:border-border">
              <span className="text-[10px] font-medium">Certificate chain</span>
              <textarea
                className="mt-2 min-h-44 w-full resize-y border border-input bg-transparent p-3 font-mono text-[10px] outline-none focus:border-ring"
                onChange={(event) => setCertificatePEM(event.target.value)}
                placeholder="Paste the PEM certificate chain"
                required
                value={certificatePEM}
              />
            </label>
            <label className="border-t border-border px-5 py-4 lg:border-t-0">
              <span className="text-[10px] font-medium">Private key</span>
              <textarea
                autoComplete="off"
                className="mt-2 min-h-44 w-full resize-y border border-input bg-transparent p-3 font-mono text-[10px] outline-none focus:border-ring"
                onChange={(event) => setPrivateKeyPEM(event.target.value)}
                placeholder="Paste the PEM private key"
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
              {submitLabel(busy === "certificate", Boolean(replacingID))}
            </Button>
          </div>
        </FormCard>
      ) : null}
    </PageStack>
  );
};
