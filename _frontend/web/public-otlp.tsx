import { Globe2, LoaderCircle } from "lucide-react";
import { useState } from "react";

import { updateServiceOTLPPublicAccess } from "@/api";
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
import {
  dedicatedTelemetryEndpoint,
  disabledTelemetryEndpoint,
  telemetryEndpointSelection,
  validTelemetryPublicPath,
} from "@/public-telemetry-endpoint";

const defaultPathPrefix = "/otel";

const updateUnavailable = (
  busy: boolean,
  changed: boolean,
  disabled: boolean,
  hostname: string,
  pathPrefix: string
) =>
  busy ||
  !changed ||
  (!disabled &&
    (!hostname || !pathPrefix || !validTelemetryPublicPath(pathPrefix)));

const PublicEndpointValue = ({ endpoint }: { endpoint?: string }) => {
  if (!endpoint) {
    return (
      <p className="col-span-2 text-[9px] leading-4 text-muted-foreground">
        Disabled. Browser exporters cannot send telemetry from outside the
        project network.
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
  if (selection === dedicatedTelemetryEndpoint) {
    return (
      <CertificateHostnameCombobox
        ariaLabel="Dedicated public OTLP hostname"
        disabled={busy}
        onChange={onChange}
        placeholder="otel.example.com"
        value={dedicatedHostname}
      />
    );
  }
  return (
    <div className="flex h-8 items-center border border-border px-2.5 text-[9px] text-muted-foreground">
      {disabled ? "No public OTLP ingress" : "Uses an existing service domain"}
    </div>
  );
};

export const PublicOTLP = ({
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
  const initialSelection = telemetryEndpointSelection(
    configuration.publicOtlpHostname,
    domains
  );
  const [selection, setSelection] = useState(initialSelection);
  const [dedicatedHostname, setDedicatedHostname] = useState(
    initialSelection === dedicatedTelemetryEndpoint
      ? (configuration.publicOtlpHostname ?? "")
      : ""
  );
  const [path, setPath] = useState(
    configuration.publicOtlpPathPrefix ?? defaultPathPrefix
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const disabled = selection === disabledTelemetryEndpoint;
  const hostname =
    selection === dedicatedTelemetryEndpoint
      ? dedicatedHostname.trim()
      : selection;
  const pathPrefix = disabled ? "" : path.trim();
  const changed =
    (disabled ? "" : hostname) !== (configuration.publicOtlpHostname ?? "") ||
    pathPrefix !== (configuration.publicOtlpPathPrefix ?? "");

  const saveUnavailable = updateUnavailable(
    busy,
    changed,
    disabled,
    hostname,
    pathPrefix
  );

  const save = async () => {
    if (saveUnavailable) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      onChanged(
        await updateServiceOTLPPublicAccess(projectID, serviceID, {
          expectedUpdatedAt: configuration.updatedAt,
          pathPrefix,
          publicHostname: disabled ? "" : hostname,
        })
      );
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to update public OTLP endpoint"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsSection
      copy="Expose OTLP HTTP/protobuf traces and logs for browser applications. The routes support CORS, gzip request bodies, and can share an existing application or Sentry domain."
      title="Browser OTLP"
    >
      <div className="mb-4 max-w-4xl divide-y divide-border border-y border-border">
        <div className="grid min-h-12 grid-cols-[8rem_minmax(0,1fr)_auto] items-center gap-3 py-2 max-sm:grid-cols-[minmax(0,1fr)_auto]">
          <span className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase max-sm:hidden">
            Public endpoint
          </span>
          <PublicEndpointValue endpoint={configuration.publicOtlpEndpoint} />
        </div>
      </div>

      <div className="grid max-w-3xl gap-2 sm:grid-cols-[minmax(13rem,0.8fr)_minmax(16rem,1.2fr)]">
        <Select
          disabled={busy}
          items={{
            [disabledTelemetryEndpoint]: "Disabled",
            ...Object.fromEntries(
              domains.map((domain) => [domain.hostname, domain.hostname])
            ),
            [dedicatedTelemetryEndpoint]: "Dedicated domain…",
          }}
          onValueChange={(value) => setSelection(String(value))}
          value={selection}
        >
          <SelectTrigger
            aria-label="Public OTLP endpoint type"
            className="w-full"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            <SelectItem value={disabledTelemetryEndpoint}>Disabled</SelectItem>
            {domains.map((domain) => (
              <SelectItem key={domain.hostname} value={domain.hostname}>
                {domain.hostname}
              </SelectItem>
            ))}
            <SelectItem value={dedicatedTelemetryEndpoint}>
              Dedicated domain…
            </SelectItem>
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
          aria-label="Public OTLP path prefix"
          autoCapitalize="none"
          autoComplete="off"
          disabled={busy || disabled}
          onChange={(event) => setPath(event.target.value)}
          placeholder={defaultPathPrefix}
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
        The prefix exposes exact /v1/traces and /v1/logs routes for POST and
        browser preflight requests. They take priority over application routes.
      </p>
      {configuration.publicOtlpEndpoint ? (
        <BrowserOTELGuide
          endpoint={configuration.publicOtlpEndpoint}
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
