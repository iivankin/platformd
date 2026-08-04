import { LoaderCircle, Network, Save } from "lucide-react";
import { useState } from "react";

import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";

export interface ObjectStorePublicAccessDraft {
  corsOrigins: string[];
  publicHostname: string;
}

export const objectStorePublicAccessDraft = (input?: {
  corsOrigins?: string[];
  publicHostname?: string;
}): ObjectStorePublicAccessDraft => ({
  corsOrigins: input?.corsOrigins ?? [],
  publicHostname: input?.publicHostname ?? "",
});

const draftsEqual = (
  left: ObjectStorePublicAccessDraft,
  right: ObjectStorePublicAccessDraft
) =>
  left.publicHostname === right.publicHostname &&
  left.corsOrigins.join("\n") === right.corsOrigins.join("\n");

const parseCorsOrigins = (value: string) =>
  value
    .split(/[\n,]/u)
    .map((origin) => origin.trim())
    .filter(Boolean);

export const ObjectStorePublicAccessSettings = ({
  onSave,
  updatedAt,
  value,
}: {
  onSave: (
    draft: ObjectStorePublicAccessDraft,
    expectedUpdatedAt: number
  ) => Promise<void>;
  updatedAt: number;
  value?: {
    corsOrigins?: string[];
    publicHostname?: string;
  };
}) => {
  const baseline = objectStorePublicAccessDraft(value);
  const [draft, setDraft] = useState(baseline);
  const [baselineAt, setBaselineAt] = useState(updatedAt);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (updatedAt !== baselineAt) {
    setBaselineAt(updatedAt);
    setDraft(baseline);
    setError(null);
  }

  const dirty = !draftsEqual(draft, baseline);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSave(draft, updatedAt);
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to save public access settings"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
      <div className="px-5 py-4">
        <div className="flex items-center gap-2">
          <Network className="size-3.5 text-muted-foreground" />
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Public access
          </h3>
        </div>
        <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
          Optional HTTPS hostname and browser CORS origins. Empty hostname keeps
          the bucket internal-only.
        </p>
      </div>
      <div className="border-t border-border lg:border-t-0 lg:border-l">
        <div className="border-b border-border px-5 py-4">
          <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Public hostname
          </span>
          <div className="mt-2">
            <CertificateHostnameCombobox
              ariaLabel="Object storage public hostname"
              id="object-store-public-hostname"
              onChange={(publicHostname) =>
                setDraft((current) => ({ ...current, publicHostname }))
              }
              placeholder="objects.example.com"
              value={draft.publicHostname}
            />
          </div>
        </div>
        <label
          className="block border-b border-border px-5 py-4"
          htmlFor="object-store-cors"
        >
          <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            CORS origins
          </span>
          <textarea
            className="mt-2 min-h-24 w-full resize-y border border-input bg-background px-2.5 py-2 text-xs leading-5 text-foreground outline-none placeholder:text-muted-foreground/70 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring"
            id="object-store-cors"
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                corsOrigins: parseCorsOrigins(event.target.value),
              }))
            }
            placeholder={"https://app.example.com\nhttps://admin.example.com"}
            value={draft.corsOrigins.join("\n")}
          />
        </label>
        <div className="flex items-center gap-3 px-5 py-4">
          <Button
            disabled={!dirty || busy}
            onClick={() => void save()}
            size="sm"
          >
            {busy ? <LoaderCircle className="animate-spin" /> : <Save />}
            Save public access
          </Button>
          {error ? (
            <p aria-live="polite" className="text-[9px] text-destructive">
              {error}
            </p>
          ) : null}
        </div>
      </div>
    </SectionCard>
  );
};
