import { Check, RefreshCw, Trash2, X } from "lucide-react";

import type { InstallationSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";

type Certificate = InstallationSettings["certificates"][number];

interface SettingsCertificateRowProperties {
  busy: boolean;
  certificate: Certificate;
  deleting: boolean;
  onCancelDelete: () => void;
  onDelete: () => void;
  onReplace: () => void;
  onStartDelete: () => void;
}

export const SettingsCertificateRow = ({
  busy,
  certificate,
  deleting,
  onCancelDelete,
  onDelete,
  onReplace,
  onStartDelete,
}: SettingsCertificateRowProperties) => (
  <SectionCard className="grid lg:grid-cols-[minmax(16rem,1fr)_minmax(12rem,0.6fr)_auto] lg:items-center">
    <div className="px-5 py-4">
      <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
        DNS names
      </p>
      <p className="mt-2 font-mono text-[10px]">
        {certificate.dnsNames.join(", ") || "No SANs"}
      </p>
    </div>
    <div className="border-y border-border px-5 py-4 lg:border-x lg:border-y-0">
      <p className="text-[9px] text-muted-foreground">
        Added {new Date(certificate.createdAt).toLocaleString()}
      </p>
      <details className="mt-2 text-[9px] text-muted-foreground">
        <summary className="cursor-pointer hover:text-foreground">
          Advanced details
        </summary>
        <p className="mt-1 truncate font-mono" title={certificate.id}>
          {certificate.id}
        </p>
      </details>
    </div>
    <div className="flex gap-1 px-4 py-3">
      <Button onClick={onReplace} size="sm" variant="ghost">
        <RefreshCw /> Replace
      </Button>
      {deleting ? (
        <>
          <Button
            disabled={busy}
            onClick={onDelete}
            size="sm"
            variant="destructive"
          >
            <Check /> Confirm
          </Button>
          <Button onClick={onCancelDelete} size="icon" variant="ghost">
            <X />
          </Button>
        </>
      ) : (
        <Button
          aria-label={`Delete certificate ${certificate.id}`}
          onClick={onStartDelete}
          size="icon"
          variant="ghost"
        >
          <Trash2 />
        </Button>
      )}
    </div>
  </SectionCard>
);
