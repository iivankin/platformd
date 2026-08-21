import { Globe2, LoaderCircle } from "lucide-react";
import { useState } from "react";

import { updateServiceOTLPTracePublicAccess } from "@/api";
import type { ServiceDomain, ServiceTelemetry } from "@/api";
import { BrowserOTELGuide } from "@/browser-otel-guide";
import { publicSentryTunnel } from "@/browser-otel-setup";
import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { CopyButton, SettingsSection } from "@/errors/settings-common";

const disabledEndpoint = "__disabled__";
const dedicatedEndpoint = "__dedicated__";
const defaultTracePath = "/otel/v1/traces";

const endpointSelection = (
  hostname: string | undefined,
  domains: ServiceDomain[]
) => {
  if (!hostname) {
    return disabledEndpoint;
  }
  if (domains.some((domain) => domain.hostname === hostname)) {
    return hostname;
  }
  return dedicatedEndpoint;
};

const updateUnavailable = (
  busy: boolean,
  changed: boolean,
  disabled: boolean,
  hostname: string,
  tracePath: string
) => busy || !changed || (!disabled && (!hostname || !tracePath));

const PublicEndpointValue = ({ endpoint }: { endpoint?: string }) => {
  if (!endpoint) {
    return (
      <p className="col-span-2 text-[9px] leading-4 text-muted-foreground">
        Disabled. Browser exporters cannot send traces from outside the project
        network.
      </p>
    );
  }
  return (
    <>
      <code className="min-w-0 overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-foreground/75">
        {endpoint}
      </code>
      <CopyButton value={endpoint} />
    </>
  );
};

const PublicHostnameInput = ({
  busy,
  dedicatedHostname,
  disabled,
  onChange,
  selection,
}: {
  busy: boolean;
  dedicatedHostname: string;
  disabled: boolean;
  onChange: (value: string) => void;
  selection: string;
}) => {
  if (selection === dedicatedEndpoint) {
    return (
      <CertificateHostnameCombobox
        ariaLabel="Dedicated public OTLP trace hostname"
        disabled={busy}
        onChange={onChange}
        placeholder="otel.example.com"
        value={dedicatedHostname}
      />
    );
  }
  return (
    <div className="flex h-8 items-center border border-border px-2.5 text-[9px] text-muted-foreground">
      {disabled ? "No public trace ingress" : "Uses an existing service domain"}
    </div>
  );
};

export const PublicOTLPTraces = ({
  configuration,
  domains,
  onChanged,
  projectID,
  serviceID,
  serviceName,
}: {
  configuration: ServiceTelemetry;
  domains: ServiceDomain[];
  onChanged: (configuration: ServiceTelemetry) => void;
  projectID: string;
  serviceID: string;
  serviceName: string;
}) => {
  const initialSelection = endpointSelection(
    configuration.publicOtlpTraceHostname,
    domains
  );
  const [selection, setSelection] = useState(initialSelection);
  const [dedicatedHostname, setDedicatedHostname] = useState(
    initialSelection === dedicatedEndpoint
      ? (configuration.publicOtlpTraceHostname ?? "")
      : ""
  );
  const [path, setPath] = useState(
    configuration.publicOtlpTracePath ?? defaultTracePath
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const disabled = selection === disabledEndpoint;
  const hostname =
    selection === dedicatedEndpoint ? dedicatedHostname.trim() : selection;
  const tracePath = disabled ? "" : path.trim();
  const changed =
    (disabled ? "" : hostname) !==
      (configuration.publicOtlpTraceHostname ?? "") ||
    tracePath !== (configuration.publicOtlpTracePath ?? "");

  const saveUnavailable = updateUnavailable(
    busy,
    changed,
    disabled,
    hostname,
    tracePath
  );

  const save = async () => {
    if (saveUnavailable) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      onChanged(
        await updateServiceOTLPTracePublicAccess(projectID, serviceID, {
          expectedUpdatedAt: configuration.updatedAt,
          publicHostname: disabled ? "" : hostname,
          tracePath,
        })
      );
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to update public OTLP traces"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsSection
      copy="Expose one exact OTLP HTTP/protobuf traces route for browser applications. The route supports CORS, gzip request bodies, and can share an existing application or Sentry domain."
      title="Browser OTLP traces"
    >
      <div className="mb-4 max-w-4xl divide-y divide-border border-y border-border">
        <div className="grid min-h-12 grid-cols-[8rem_minmax(0,1fr)_auto] items-center gap-3 py-2 max-sm:grid-cols-[minmax(0,1fr)_auto]">
          <span className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase max-sm:hidden">
            Public endpoint
          </span>
          <PublicEndpointValue
            endpoint={configuration.publicOtlpTraceEndpoint}
          />
        </div>
      </div>

      <div className="grid max-w-3xl gap-2 sm:grid-cols-[minmax(13rem,0.8fr)_minmax(16rem,1.2fr)]">
        <Select
          disabled={busy}
          items={{
            [disabledEndpoint]: "Disabled",
            ...Object.fromEntries(
              domains.map((domain) => [domain.hostname, domain.hostname])
            ),
            [dedicatedEndpoint]: "Dedicated domain…",
          }}
          onValueChange={(value) => setSelection(String(value))}
          value={selection}
        >
          <SelectTrigger
            aria-label="Public OTLP trace endpoint type"
            className="w-full"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            <SelectItem value={disabledEndpoint}>Disabled</SelectItem>
            {domains.map((domain) => (
              <SelectItem key={domain.hostname} value={domain.hostname}>
                {domain.hostname}
              </SelectItem>
            ))}
            <SelectItem value={dedicatedEndpoint}>Dedicated domain…</SelectItem>
          </SelectContent>
        </Select>
        <PublicHostnameInput
          busy={busy}
          dedicatedHostname={dedicatedHostname}
          disabled={disabled}
          onChange={setDedicatedHostname}
          selection={selection}
        />
      </div>
      <div className="mt-2 grid max-w-3xl grid-cols-[minmax(0,1fr)_auto] gap-2 max-sm:grid-cols-1">
        <Input
          aria-label="Public OTLP trace path"
          autoCapitalize="none"
          autoComplete="off"
          disabled={busy || disabled}
          onChange={(event) => setPath(event.target.value)}
          placeholder={defaultTracePath}
          spellCheck={false}
          value={path}
        />
        <Button disabled={saveUnavailable} onClick={() => void save()}>
          {busy ? <LoaderCircle className="animate-spin" /> : <Globe2 />}
          {busy ? "Updating…" : "Save endpoint"}
        </Button>
      </div>
      {error ? (
        <p aria-live="polite" className="mt-2 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      <p className="mt-3 max-w-3xl text-[9px] leading-4 text-muted-foreground">
        The exact path accepts POST and browser preflight requests, then
        forwards payloads to /v1/traces. It takes priority over an application
        route with the same path.
      </p>
      {configuration.publicOtlpTraceEndpoint ? (
        <BrowserOTELGuide
          endpoint={configuration.publicOtlpTraceEndpoint}
          sentryDsn={configuration.publicDsn}
          sentryTunnel={publicSentryTunnel(
            configuration.publicDsn,
            configuration.browserTunnelPath
          )}
          serviceName={serviceName}
        />
      ) : null}
    </SettingsSection>
  );
};
