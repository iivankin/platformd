import { Check, Clipboard, Globe2, LoaderCircle } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import {
  fetchService,
  fetchServiceTelemetry,
  updateServiceTelemetryPublicAccess,
} from "@/api";
import type { Service, ServiceTelemetry } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ErrorsApp } from "@/errors/errors-app";
import { CopyButton, SettingsSection } from "@/errors/settings-common";
import { ApplicationSettingsView } from "@/errors/settings-view";
import type { App, TelemetryMetadata } from "@/errors/types";
import {
  adminOrigin,
  projectNameFromInternalHostname,
} from "@/github-action-example-dialog";
import { ResourceLogs } from "@/resource-logs";
import { SentryCloudflareGeoIpHint } from "@/sentry-cloudflare-geoip-hint";
import { ServiceMetrics } from "@/service-metrics";
import { ServiceTraces } from "@/service-traces";
import { TelemetryWorkspace } from "@/telemetry-workspace";

const publicBase = (dsn: string) => {
  const parsed = new URL(dsn);
  return `${parsed.protocol}//${parsed.host}`;
};

const PublicSentryEndpoint = ({
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
  const [hostname, setHostname] = useState(configuration.publicHostname ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

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
      setHostname(updated.publicHostname ?? "");
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
      copy="platformd adds the internal DSN to SENTRY_DSN for every deployment by default. Browser SDKs and external workers require a public domain."
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
      <div className="flex max-w-3xl items-start gap-2 max-sm:flex-col">
        <div className="w-full min-w-0">
          <Input
            aria-label="Public Sentry hostname"
            disabled={busy}
            onChange={(event) => setHostname(event.target.value)}
            placeholder="errors.example.com"
            value={hostname}
          />
          {error ? (
            <p aria-live="polite" className="mt-2 text-[10px] text-destructive">
              {error}
            </p>
          ) : null}
        </div>
        <Button disabled={busy} onClick={() => void save()}>
          {busy ? <LoaderCircle className="animate-spin" /> : <Globe2 />}
          {busy ? "Updating…" : "Update domain"}
        </Button>
      </div>
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
  const [service, setService] = useState<Service>();
  const [error, setError] = useState("");
  const [toast, setToast] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [nextService, nextConfiguration] = await Promise.all([
          fetchService(projectID, serviceID, controller.signal),
          fetchServiceTelemetry(projectID, serviceID, controller.signal),
        ]);
        setService(nextService);
        setConfiguration(nextConfiguration);
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
                  <PublicSentryEndpoint
                    configuration={configuration}
                    onChanged={setConfiguration}
                    projectID={projectID}
                    serviceID={serviceID}
                  />
                  <TelemetryIngestionEndpoint
                    endpoint={configuration.internalOtlpEndpoint}
                  />
                </>
              }
              telemetry={metadata}
            />
          ),
          traces: <ServiceTraces projectID={projectID} serviceID={serviceID} />,
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
