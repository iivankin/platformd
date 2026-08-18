import {
  Check,
  Clipboard,
  Globe2,
  LoaderCircle,
  Route as RouteIcon,
} from "lucide-react";
import { useQueryState, useQueryStates } from "nuqs";
import type { ReactNode } from "react";
import { useCallback, useEffect, useMemo, useState } from "react";

import {
  fetchService,
  fetchServiceDomains,
  fetchServiceTelemetry,
  updateServiceTelemetryBrowserTunnel,
  updateServiceTelemetryPublicAccess,
} from "@/api";
import type { Service, ServiceDomain, ServiceTelemetry } from "@/api";
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
import { ErrorsApp } from "@/errors/errors-app";
import { CopyButton, SettingsSection } from "@/errors/settings-common";
import { ApplicationSettingsView } from "@/errors/settings-view";
import type { App, TelemetryMetadata } from "@/errors/types";
import {
  adminOrigin,
  projectNameFromInternalHostname,
} from "@/github-action-example-dialog";
import { ServiceAnalyticsSnippet } from "@/project-analytics";
import { ResourceLogs } from "@/resource-logs";
import { SentryCloudflareGeoIpHint } from "@/sentry-cloudflare-geoip-hint";
import { ServiceMetrics } from "@/service-metrics";
import { ServiceTraces } from "@/service-traces";
import {
  errorDetailQueryParsers,
  logQueryParsers,
  telemetryViewParser,
  traceQueryParsers,
} from "@/telemetry-query-state";
import { TelemetryWorkspace } from "@/telemetry-workspace";

const publicBase = (dsn: string) => {
  const parsed = new URL(dsn);
  return `${parsed.protocol}//${parsed.host}`;
};

const disabledPublicEndpoint = "__disabled__";
const dedicatedPublicEndpoint = "__dedicated__";

const publicEndpointSelection = (
  configuration: ServiceTelemetry,
  domains: ServiceDomain[]
) => {
  if (!configuration.publicHostname) {
    return disabledPublicEndpoint;
  }
  if (
    domains.some((domain) => domain.hostname === configuration.publicHostname)
  ) {
    return configuration.publicHostname;
  }
  return dedicatedPublicEndpoint;
};

const PublicSentryEndpoint = ({
  configuration,
  domains,
  onChanged,
  projectID,
  serviceID,
  tunnelSettings,
}: {
  configuration: ServiceTelemetry;
  domains: ServiceDomain[];
  onChanged: (configuration: ServiceTelemetry) => void;
  projectID: string;
  serviceID: string;
  tunnelSettings?: ReactNode;
}) => {
  const initialSelection = publicEndpointSelection(configuration, domains);
  const [selection, setSelection] = useState(initialSelection);
  const [dedicatedHostname, setDedicatedHostname] = useState(
    initialSelection === dedicatedPublicEndpoint
      ? (configuration.publicHostname ?? "")
      : ""
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  let hostname = selection;
  if (selection === disabledPublicEndpoint) {
    hostname = "";
  } else if (selection === dedicatedPublicEndpoint) {
    hostname = dedicatedHostname.trim();
  }

  const save = async () => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      const updated = await updateServiceTelemetryPublicAccess(
        projectID,
        serviceID,
        { expectedUpdatedAt: configuration.updatedAt, publicHostname: hostname }
      );
      onChanged(updated);
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to update the public Sentry endpoint"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsSection
      copy="platformd adds the internal DSN to SENTRY_DSN for every deployment by default. Browser SDKs and external workers can use an existing service domain or a dedicated telemetry domain."
      title="Sentry endpoints"
    >
      <div className="mb-4 max-w-4xl divide-y divide-border border-y border-border">
        <div className="grid min-h-12 grid-cols-[8rem_minmax(0,1fr)_auto] items-center gap-3 py-2 max-sm:grid-cols-[minmax(0,1fr)_auto]">
          <span className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase max-sm:hidden">
            Project network
          </span>
          <code className="min-w-0 overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-foreground/75">
            {configuration.internalDsn}
          </code>
          <CopyButton value={configuration.internalDsn} />
        </div>
        <div className="grid min-h-12 grid-cols-[8rem_minmax(0,1fr)_auto] items-center gap-3 py-2 max-sm:grid-cols-[minmax(0,1fr)_auto]">
          <span className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase max-sm:hidden">
            Public internet
          </span>
          {configuration.publicDsn ? (
            <>
              <code className="min-w-0 overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-foreground/75">
                {configuration.publicDsn}
              </code>
              <CopyButton value={configuration.publicDsn} />
            </>
          ) : (
            <p className="col-span-2 text-[9px] leading-4 text-muted-foreground">
              Add a domain below before connecting a browser SDK or external
              runtime.
            </p>
          )}
        </div>
      </div>
      <div className="grid max-w-3xl gap-2 sm:grid-cols-[minmax(13rem,0.8fr)_minmax(16rem,1.2fr)_auto]">
        <Select
          disabled={busy}
          items={{
            [disabledPublicEndpoint]: "Disabled",
            ...Object.fromEntries(
              domains.map((domain) => [domain.hostname, domain.hostname])
            ),
            [dedicatedPublicEndpoint]: "Dedicated domain…",
          }}
          onValueChange={(value) => setSelection(String(value))}
          value={selection}
        >
          <SelectTrigger
            aria-label="Public telemetry endpoint type"
            className="w-full"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            <SelectItem value={disabledPublicEndpoint}>Disabled</SelectItem>
            {domains.map((domain) => (
              <SelectItem key={domain.hostname} value={domain.hostname}>
                {domain.hostname}
              </SelectItem>
            ))}
            <SelectItem value={dedicatedPublicEndpoint}>
              Dedicated domain…
            </SelectItem>
          </SelectContent>
        </Select>
        <div className="min-w-0">
          {selection === dedicatedPublicEndpoint ? (
            <CertificateHostnameCombobox
              ariaLabel="Dedicated public telemetry hostname"
              disabled={busy}
              onChange={setDedicatedHostname}
              placeholder="errors.example.com"
              value={dedicatedHostname}
            />
          ) : (
            <div className="flex h-8 items-center border border-border px-2.5 text-[9px] text-muted-foreground">
              {selection === disabledPublicEndpoint
                ? "No public Sentry ingress"
                : "Uses an existing service domain"}
            </div>
          )}
          {error ? (
            <p aria-live="polite" className="mt-2 text-[10px] text-destructive">
              {error}
            </p>
          ) : null}
        </div>
        <Button
          disabled={
            busy ||
            (selection === dedicatedPublicEndpoint && hostname.length === 0)
          }
          onClick={() => void save()}
        >
          {busy ? <LoaderCircle className="animate-spin" /> : <Globe2 />}
          {busy ? "Updating…" : "Save endpoint"}
        </Button>
      </div>
      <p className="mt-3 max-w-3xl text-[9px] leading-4 text-muted-foreground">
        Platformd reserves only the exact Sentry SDK and artifact-upload routes
        on this hostname. Every other application path keeps its current
        behavior.
      </p>
      {tunnelSettings}
      <div className="mt-4 max-w-3xl">
        <SentryCloudflareGeoIpHint />
      </div>
    </SettingsSection>
  );
};

const TelemetryIngestionEndpoint = ({ endpoint }: { endpoint: string }) => {
  const [copied, setCopied] = useState(false);
  const [copyError, setCopyError] = useState("");
  const environment = [
    `OTEL_EXPORTER_OTLP_ENDPOINT=${endpoint}`,
    "OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
  ].join("\n");

  const copy = async () => {
    setCopyError("");
    try {
      await navigator.clipboard.writeText(environment);
      setCopied(true);
    } catch {
      setCopyError("Unable to copy the telemetry ingestion configuration");
    }
  };

  return (
    <SettingsSection
      copy="platformd adds this OTLP endpoint, HTTP/protobuf protocol, service name, and deployment resource attributes to every deployment by default. Explicit service variables override these defaults."
      title="Telemetry ingestion"
    >
      <div className="relative max-w-3xl border-y border-border bg-muted/20">
        <pre className="overflow-x-auto px-4 py-3 pr-24 text-[10px] leading-5 text-foreground/75">
          {environment}
        </pre>
        <div className="absolute top-2 right-2">
          <Button onClick={() => void copy()} size="sm" variant="outline">
            {copied ? <Check /> : <Clipboard />}
            {copied ? "Copied" : "Copy"}
          </Button>
        </div>
      </div>
      <p className="mt-3 max-w-3xl text-[9px] leading-4 text-muted-foreground">
        The endpoint accepts OTLP HTTP/protobuf at /v1/traces, /v1/metrics, and
        /v1/logs. Copy this snippet only for a runtime configured outside
        platformd.
      </p>
      {copyError ? (
        <p aria-live="polite" className="mt-2 text-[10px] text-destructive">
          {copyError}
        </p>
      ) : null}
    </SettingsSection>
  );
};

const BrowserTunnelPath = ({
  configuration,
  onChanged,
  projectID,
  serviceID,
}: {
  configuration: ServiceTelemetry;
  onChanged: (configuration: ServiceTelemetry) => void;
  projectID: string;
  serviceID: string;
}) => {
  const initialPath = configuration.browserTunnelPath ?? "";
  const [value, setValue] = useState(initialPath);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const tunnelPath = value.trim();
  const savedTunnelURL =
    configuration.publicDsn && initialPath
      ? `${publicBase(configuration.publicDsn)}${initialPath}`
      : "";

  const save = async () => {
    if (busy || tunnelPath === initialPath) {
      return;
    }
    setBusy(true);
    setError("");
    try {
      onChanged(
        await updateServiceTelemetryBrowserTunnel(projectID, serviceID, {
          browserTunnelPath: tunnelPath,
          expectedUpdatedAt: configuration.updatedAt,
        })
      );
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to update the browser tunnel"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="mt-5 max-w-3xl border-t border-border pt-4">
      <div className="mb-3">
        <h3 className="text-[11px] text-foreground">Browser SDK tunnel</h3>
        <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
          Optionally accept browser envelopes at a custom path on{" "}
          <span className="text-foreground/75">
            {configuration.publicHostname}
          </span>
          .
        </p>
      </div>
      <div className="grid max-w-3xl grid-cols-[minmax(0,1fr)_auto] gap-2 max-sm:grid-cols-1">
        <Input
          aria-label="Browser SDK tunnel path"
          autoCapitalize="none"
          autoComplete="off"
          disabled={busy}
          onChange={(event) => setValue(event.target.value)}
          placeholder="/sentry-tunnel"
          spellCheck={false}
          value={value}
        />
        <Button
          disabled={busy || tunnelPath === initialPath}
          onClick={() => void save()}
        >
          {busy ? <LoaderCircle className="animate-spin" /> : <RouteIcon />}
          {busy ? "Updating…" : "Save tunnel"}
        </Button>
      </div>
      {error ? (
        <p aria-live="polite" className="mt-2 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      <p className="mt-3 max-w-3xl text-[9px] leading-4 text-muted-foreground">
        Leave empty to use the normal DSN endpoint. The exact POST route takes
        priority over an application route with the same path.
      </p>
      {initialPath ? (
        <div className="mt-4 flex max-w-3xl items-center justify-between gap-4 border-y border-border bg-muted/20 px-3 py-2.5">
          <code className="min-w-0 overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-foreground/75">
            tunnel: {JSON.stringify(savedTunnelURL)}
          </code>
          <CopyButton value={`tunnel: ${JSON.stringify(savedTunnelURL)}`} />
        </div>
      ) : null}
    </div>
  );
};

export const ServiceTelemetryWorkspace = ({
  cpuMillicores,
  memoryBytes,
  projectID,
  serviceID,
}: {
  cpuMillicores?: number;
  memoryBytes?: number;
  projectID: string;
  serviceID: string;
}) => {
  const [configuration, setConfiguration] = useState<ServiceTelemetry>();
  const [domains, setDomains] = useState<ServiceDomain[]>([]);
  const [service, setService] = useState<Service>();
  const [error, setError] = useState("");
  const [toast, setToast] = useState("");
  const [, setTelemetryView] = useQueryState("telemetry", telemetryViewParser);
  const [, setLogState] = useQueryStates(logQueryParsers);
  const [, setTraceState] = useQueryStates(traceQueryParsers);
  const [, setErrorState] = useQueryStates(errorDetailQueryParsers);
  const openTrace = useCallback(
    (traceID: string, segmentID?: string) => {
      void Promise.all([
        setTraceState(
          { trace: traceID, traceSegment: segmentID ?? null },
          { history: "push" }
        ),
        setTelemetryView("traces", { history: "push" }),
      ]);
    },
    [setTelemetryView, setTraceState]
  );
  const openTraceLogs = useCallback(
    (traceID: string, spanID?: string) => {
      void Promise.all([
        setLogState({ logSpan: spanID ?? null, logTrace: traceID }),
        setTelemetryView("logs", { history: "push" }),
      ]);
    },
    [setLogState, setTelemetryView]
  );
  const openError = useCallback(
    (issueID: string, _eventID?: string) => {
      void Promise.all([
        setErrorState({
          errorEvent: null,
          errorIssue: issueID,
        }),
        setTelemetryView("errors", { history: "push" }),
      ]);
    },
    [setErrorState, setTelemetryView]
  );

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [nextService, nextConfiguration, nextDomains] = await Promise.all(
          [
            fetchService(projectID, serviceID, controller.signal),
            fetchServiceTelemetry(projectID, serviceID, controller.signal),
            fetchServiceDomains(projectID, serviceID, controller.signal),
          ]
        );
        setService(nextService);
        setConfiguration(nextConfiguration);
        setDomains(nextDomains);
        setError("");
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load service telemetry"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, serviceID]);

  useEffect(() => {
    if (!toast) {
      return;
    }
    const timeout = setTimeout(() => setToast(""), 2600);
    return () => clearTimeout(timeout);
  }, [toast]);

  const refresh = async () => {
    setConfiguration(await fetchServiceTelemetry(projectID, serviceID));
  };
  const app = useMemo<App | undefined>(
    () =>
      configuration && service
        ? {
            browserTunnelPath: configuration.browserTunnelPath,
            id: service.id,
            internalDsn: configuration.internalDsn,
            name: service.name,
            publicDsn: configuration.publicDsn,
            slug: service.name,
            webhooks: configuration.webhooks,
          }
        : undefined,
    [configuration, service]
  );
  const metadata = useMemo<TelemetryMetadata>(
    () => ({
      controlPlaneUrl: adminOrigin(),
      projectName: configuration
        ? projectNameFromInternalHostname(configuration.internalHostname)
        : "project",
      publicUrl: configuration?.publicDsn
        ? publicBase(configuration.publicDsn)
        : undefined,
      slug: "platformd",
    }),
    [configuration]
  );
  if (error) {
    return (
      <div className="grid min-h-[28rem] place-items-center border border-destructive/35 p-8 text-center text-[10px] text-destructive">
        {error}
      </div>
    );
  }
  if (!(configuration && app)) {
    return (
      <div className="grid min-h-[28rem] place-items-center text-[10px] text-muted-foreground">
        <span className="flex items-center gap-2">
          <LoaderCircle className="size-3 animate-spin" /> Opening telemetry
        </span>
      </div>
    );
  }

  return (
    <>
      <TelemetryWorkspace
        views={{
          errors: (
            <ErrorsApp
              apiBasePath={`/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}`}
              app={app}
              onOpenTrace={openTrace}
            />
          ),
          logs: (
            <ResourceLogs
              kind="service"
              projectID={projectID}
              resourceID={serviceID}
            />
          ),
          metrics: (
            <ServiceMetrics
              cpuMillicores={cpuMillicores}
              memoryBytes={memoryBytes}
              projectID={projectID}
              serviceID={serviceID}
            />
          ),
          settings: (
            <ApplicationSettingsView
              app={app}
              key={app.id}
              notify={setToast}
              refresh={refresh}
              settingsHeader={
                <>
                  <ServiceAnalyticsSnippet
                    projectID={projectID}
                    trackedBy={configuration.trackedBy}
                  />
                  <PublicSentryEndpoint
                    configuration={configuration}
                    domains={domains}
                    key={`${configuration.updatedAt}:${domains.map((domain) => domain.hostname).join(",")}`}
                    onChanged={setConfiguration}
                    projectID={projectID}
                    serviceID={serviceID}
                    tunnelSettings={
                      configuration.publicHostname ? (
                        <BrowserTunnelPath
                          configuration={configuration}
                          onChanged={setConfiguration}
                          projectID={projectID}
                          serviceID={serviceID}
                        />
                      ) : undefined
                    }
                  />
                  <TelemetryIngestionEndpoint
                    endpoint={configuration.internalOtlpEndpoint}
                  />
                </>
              }
              telemetry={metadata}
            />
          ),
          traces: (
            <ServiceTraces
              onOpenError={openError}
              onOpenLogs={openTraceLogs}
              projectID={projectID}
              serviceID={serviceID}
            />
          ),
        }}
      />
      {toast ? (
        <output
          aria-live="polite"
          className="fixed right-4 bottom-4 z-[70] max-w-sm border border-border bg-foreground px-3 py-2 text-[10px] text-background shadow-xl"
        >
          {toast}
        </output>
      ) : null}
    </>
  );
};
