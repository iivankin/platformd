import { Globe, Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import type { ServiceDomainDraft } from "@/service-settings-model";

interface ServiceDomainsProperties {
  disabled?: boolean;
  domains: ServiceDomainDraft[];
  onChanged: (domains: ServiceDomainDraft[]) => void;
}

const validPort = (port: number) =>
  Number.isInteger(port) && port >= 1 && port <= 65_535;

export const ServiceDomains = ({
  disabled = false,
  domains,
  onChanged,
}: ServiceDomainsProperties) => {
  const [hostname, setHostname] = useState("");
  const [targetPort, setTargetPort] = useState(0);
  const [error, setError] = useState<string>();

  const commit = (domain: ServiceDomainDraft) => {
    onChanged(
      [
        ...domains.filter((current) => current.hostname !== domain.hostname),
        domain,
      ].toSorted((left, right) => left.hostname.localeCompare(right.hostname))
    );
  };

  const add = () => {
    const normalizedHostname = hostname.trim().toLocaleLowerCase();
    if (disabled || normalizedHostname === "" || !validPort(targetPort)) {
      return;
    }
    setError(undefined);
    commit({ hostname: normalizedHostname, targetPort });
    setHostname("");
    setTargetPort(0);
  };

  const remove = (domain: ServiceDomainDraft) =>
    onChanged(
      domains.filter((current) => current.hostname !== domain.hostname)
    );

  return (
    <SectionCard>
      <header className="flex min-h-14 items-center justify-between gap-4 bg-muted/25 px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">HTTP domains</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Route each HTTPS hostname to its own container port.
          </p>
        </div>
        <Globe className="size-4 shrink-0 text-muted-foreground" />
      </header>

      {domains.length ? (
        <div className="border-t border-border">
          {domains.map((domain) => (
            <div
              className="grid min-h-12 grid-cols-[minmax(0,1fr)_7rem_2.5rem] items-center border-b border-border px-5 last:border-b-0"
              key={domain.hostname}
            >
              <span className="truncate text-[10px]">{domain.hostname}</span>
              <div className="flex items-center gap-1 text-[9px] text-muted-foreground">
                <span>→ :</span>
                <Input
                  aria-label={`Container port for ${domain.hostname}`}
                  className="h-7 min-w-0 px-2 text-[9px]"
                  disabled={disabled}
                  max={65_535}
                  min={1}
                  onChange={(event) =>
                    commit({
                      ...domain,
                      targetPort: Number(event.target.value),
                    })
                  }
                  type="number"
                  value={domain.targetPort}
                />
              </div>
              <Button
                aria-label={`Remove ${domain.hostname}`}
                disabled={disabled}
                onClick={() => remove(domain)}
                size="icon"
                variant="ghost"
              >
                <Trash2 />
              </Button>
            </div>
          ))}
        </div>
      ) : (
        <p className="border-y border-dashed border-border px-5 py-4 text-[10px] text-muted-foreground">
          No public domain attached.
        </p>
      )}

      <div className="grid grid-cols-[minmax(0,1fr)_8rem_auto] gap-2 bg-muted/10 px-5 py-3">
        <CertificateHostnameCombobox
          disabled={disabled}
          onChange={(value) => {
            setHostname(value);
          }}
          value={hostname}
        />
        <Input
          aria-label="Container port"
          disabled={disabled}
          max={65_535}
          min={1}
          onChange={(event) => setTargetPort(Number(event.target.value))}
          placeholder="Container port"
          type="number"
          value={targetPort || ""}
        />
        <Button
          disabled={
            disabled || hostname.trim() === "" || !validPort(targetPort)
          }
          onClick={add}
          size="sm"
          type="button"
        >
          <Plus /> Add domain
        </Button>
      </div>

      {error ? (
        <p
          aria-live="polite"
          className="border-t border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </SectionCard>
  );
};
