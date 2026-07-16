import { ArrowRightLeft, Globe, Plus, Trash2 } from "lucide-react";
import { useState } from "react";

import { APIError, attachServiceDomain, detachServiceDomain } from "@/api";
import type { ServiceDomain } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface ServiceDomainsProperties {
  domains: ServiceDomain[];
  onChanged: (domains: ServiceDomain[]) => void;
  onPortDraftChange: (hostname: string, port: number) => void;
  portDrafts: Record<string, number>;
  projectID: string;
  serviceID: string;
}

const validPort = (port: number) =>
  Number.isInteger(port) && port >= 1 && port <= 65_535;

export const ServiceDomains = ({
  domains,
  onChanged,
  onPortDraftChange,
  portDrafts,
  projectID,
  serviceID,
}: ServiceDomainsProperties) => {
  const [hostname, setHostname] = useState("");
  const [targetPort, setTargetPort] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [moveConflict, setMoveConflict] = useState<ServiceDomain>();

  const commit = (domain: ServiceDomain) => {
    onChanged(
      [
        ...domains.filter((current) => current.hostname !== domain.hostname),
        domain,
      ].toSorted((left, right) => left.hostname.localeCompare(right.hostname))
    );
    onPortDraftChange(domain.hostname, domain.targetPort);
  };

  const attach = async (move: boolean) => {
    if (busy || hostname.trim() === "" || !validPort(targetPort)) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      commit(
        await attachServiceDomain(
          projectID,
          serviceID,
          hostname,
          targetPort,
          move
        )
      );
      setHostname("");
      setTargetPort(0);
      setMoveConflict(undefined);
    } catch (attachError) {
      if (
        attachError instanceof APIError &&
        attachError.code === "domain_conflict" &&
        attachError.domain
      ) {
        setMoveConflict(attachError.domain);
      }
      setError(
        attachError instanceof Error
          ? attachError.message
          : "Unable to attach domain"
      );
    } finally {
      setBusy(false);
    }
  };

  const remove = async (domain: ServiceDomain) => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await detachServiceDomain(projectID, serviceID, domain.hostname);
      onChanged(
        domains.filter((current) => current.hostname !== domain.hostname)
      );
    } catch (removeError) {
      setError(
        removeError instanceof Error
          ? removeError.message
          : "Unable to remove domain"
      );
    } finally {
      setBusy(false);
    }
  };

  const submit = () => {
    setMoveConflict(undefined);
    void attach(false);
  };

  return (
    <section className="border-b border-border">
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
          {domains.map((domain) => {
            const draft = portDrafts[domain.hostname] ?? domain.targetPort;
            return (
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
                    disabled={busy}
                    max={65_535}
                    min={1}
                    onChange={(event) =>
                      onPortDraftChange(
                        domain.hostname,
                        Number(event.target.value)
                      )
                    }
                    type="number"
                    value={draft}
                  />
                </div>
                <Button
                  aria-label={`Remove ${domain.hostname}`}
                  disabled={busy}
                  onClick={() => void remove(domain)}
                  size="icon"
                  variant="ghost"
                >
                  <Trash2 />
                </Button>
              </div>
            );
          })}
        </div>
      ) : (
        <p className="border-y border-dashed border-border px-5 py-4 text-[10px] text-muted-foreground">
          No public domain attached.
        </p>
      )}

      <div className="grid grid-cols-[minmax(0,1fr)_8rem_auto] gap-2 bg-muted/10 px-5 py-3">
        <Input
          aria-label="Public hostname"
          autoCapitalize="none"
          autoComplete="off"
          disabled={busy}
          onChange={(event) => {
            setHostname(event.target.value);
            setMoveConflict(undefined);
          }}
          placeholder="api.example.com"
          spellCheck={false}
          value={hostname}
        />
        <Input
          aria-label="Container port"
          disabled={busy}
          max={65_535}
          min={1}
          onChange={(event) => setTargetPort(Number(event.target.value))}
          placeholder="Container port"
          type="number"
          value={targetPort || ""}
        />
        <Button
          disabled={busy || hostname.trim() === "" || !validPort(targetPort)}
          onClick={submit}
          size="sm"
          type="button"
        >
          <Plus /> Add domain
        </Button>
      </div>

      {moveConflict ? (
        <div className="border-t border-amber-500/40 bg-amber-500/5 px-5 py-3">
          <p className="text-[10px] leading-4 text-muted-foreground">
            Attached to {moveConflict.serviceName ?? "another service"}
            {moveConflict.projectName ? ` in ${moveConflict.projectName}` : ""}.
          </p>
          <Button
            className="mt-2"
            disabled={busy}
            onClick={() => void attach(true)}
            size="sm"
            type="button"
            variant="outline"
          >
            <ArrowRightLeft /> Move here
          </Button>
        </div>
      ) : null}
      {error ? (
        <p
          aria-live="polite"
          className="border-t border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </section>
  );
};
