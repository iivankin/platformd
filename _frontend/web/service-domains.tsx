import { ArrowRightLeft, Globe, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { APIError, attachServiceDomain, detachServiceDomain } from "@/api";
import type { ServiceDomain } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface ServiceDomainsProperties {
  domains: ServiceDomain[];
  onChanged: (domains: ServiceDomain[]) => void;
  projectID: string;
  serviceID: string;
  targetPort?: number;
}

export const ServiceDomains = ({
  domains,
  onChanged,
  projectID,
  serviceID,
  targetPort,
}: ServiceDomainsProperties) => {
  const [hostname, setHostname] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [moveConflict, setMoveConflict] = useState<ServiceDomain>();

  const attach = async (move: boolean) => {
    if (busy || hostname.trim() === "") {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const attached = await attachServiceDomain(
        projectID,
        serviceID,
        hostname,
        move
      );
      onChanged(
        [
          ...domains.filter((domain) => domain.hostname !== attached.hostname),
          attached,
        ].toSorted((left, right) => left.hostname.localeCompare(right.hostname))
      );
      setHostname("");
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

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setMoveConflict(undefined);
    void attach(false);
  };

  return (
    <section className="border-b border-border px-4 py-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Public domains
          </h3>
          <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
            Exact HTTPS hostnames routed to port {targetPort ?? "—"}.
          </p>
        </div>
        <Globe className="size-4 shrink-0 text-muted-foreground" />
      </div>

      {domains.length > 0 ? (
        <div className="mt-3 border-t border-border">
          {domains.map((domain) => (
            <div
              className="flex min-h-10 items-center gap-3 border-b border-border"
              key={domain.hostname}
            >
              <span className="min-w-0 flex-1 truncate text-[10px]">
                {domain.hostname}
              </span>
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
          ))}
        </div>
      ) : (
        <p className="mt-3 border-y border-dashed border-border py-3 text-[10px] text-muted-foreground">
          No public domain attached.
        </p>
      )}

      <form className="mt-3 flex gap-2" onSubmit={submit}>
        <Input
          aria-label="Public hostname"
          autoCapitalize="none"
          autoComplete="off"
          disabled={busy || !targetPort}
          onChange={(event) => {
            setHostname(event.target.value);
            setMoveConflict(undefined);
          }}
          placeholder="api.example.com"
          spellCheck={false}
          value={hostname}
        />
        <Button
          disabled={busy || !targetPort || hostname.trim() === ""}
          size="sm"
          type="submit"
        >
          <Plus />
          Add
        </Button>
      </form>

      {moveConflict ? (
        <div className="mt-3 border-l-2 border-amber-500 pl-3">
          <p className="text-[10px] leading-4 text-muted-foreground">
            Attached to {moveConflict.serviceName ?? "another service"}
            {moveConflict.projectName ? ` in ${moveConflict.projectName}` : ""}.
            Moving it here switches the route atomically.
          </p>
          <Button
            className="mt-2"
            disabled={busy}
            onClick={() => void attach(true)}
            size="sm"
            type="button"
            variant="outline"
          >
            <ArrowRightLeft />
            Move here
          </Button>
        </div>
      ) : null}
      {error ? (
        <p aria-live="polite" className="mt-2 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </section>
  );
};
