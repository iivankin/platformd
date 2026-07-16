import { Network, Save, Trash2 } from "lucide-react";
import { useState } from "react";

import type {
  ImageCredential,
  Service,
  ServiceDomain,
  ServiceListener,
  Volume,
} from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  parseServiceConfiguration,
  ServiceConfiguration,
  serviceConfigurationDraft,
} from "@/service-configuration";
import type { ServiceConfigurationValues } from "@/service-configuration";
import { ServiceDomains } from "@/service-domains";
import { serviceListenerKey, ServiceListeners } from "@/service-listeners";
import { ServiceVolumes } from "@/service-volumes";

export interface ServiceSettingsValues extends ServiceConfigurationValues {
  domains: ServiceDomain[];
  listeners: ServiceListener[];
  volumeMounts: Service["volumeMounts"];
}

interface ServiceSettingsProperties {
  actionError: string | null;
  busy: boolean;
  credentials: ImageCredential[];
  domains: ServiceDomain[];
  embeddedRegistryHost: string;
  internalHostname: string;
  listeners: ServiceListener[];
  onCredentialCreated: (credential: ImageCredential) => void;
  onDelete: () => Promise<boolean>;
  onDomainsChange: (domains: ServiceDomain[]) => void;
  onListenersChange: (listeners: ServiceListener[]) => void;
  onSave: (values: ServiceSettingsValues) => Promise<boolean>;
  onVolumesChange: (volumes: Volume[]) => void;
  projectID: string;
  service: Service;
  serviceID: string;
  volumes: Volume[];
}

export const ServiceSettings = ({
  actionError,
  busy,
  credentials,
  domains,
  embeddedRegistryHost,
  internalHostname,
  listeners,
  onCredentialCreated,
  onDelete,
  onDomainsChange,
  onListenersChange,
  onSave,
  onVolumesChange,
  projectID,
  service,
  serviceID,
  volumes,
}: ServiceSettingsProperties) => {
  const [configuration, setConfiguration] = useState(() =>
    serviceConfigurationDraft(service)
  );
  const [volumeMounts, setVolumeMounts] = useState(service.volumeMounts);
  const [domainPorts, setDomainPorts] = useState<Record<string, number>>(() =>
    Object.fromEntries(
      domains.map((domain) => [domain.hostname, domain.targetPort])
    )
  );
  const [listenerPorts, setListenerPorts] = useState<Record<string, number>>(
    () =>
      Object.fromEntries(
        listeners.map((listener) => [
          serviceListenerKey(listener),
          listener.targetPort,
        ])
      )
  );
  const [error, setError] = useState<string>();
  const [deleting, setDeleting] = useState(false);
  const [confirmation, setConfirmation] = useState("");

  const changedDomains = domains.map((domain) => ({
    ...domain,
    targetPort: domainPorts[domain.hostname] ?? domain.targetPort,
  }));
  const changedListeners = listeners.map((listener) => ({
    ...listener,
    targetPort:
      listenerPorts[serviceListenerKey(listener)] ?? listener.targetPort,
  }));

  const save = async () => {
    try {
      const saved = await onSave({
        ...parseServiceConfiguration(
          configuration,
          credentials,
          embeddedRegistryHost
        ),
        domains: changedDomains,
        listeners: changedListeners,
        volumeMounts,
      });
      if (saved) {
        setError(undefined);
      }
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Invalid service settings"
      );
    }
  };

  const updateDomains = (next: ServiceDomain[]) => {
    setDomainPorts((current) =>
      Object.fromEntries(
        next.map((domain) => [
          domain.hostname,
          current[domain.hostname] ?? domain.targetPort,
        ])
      )
    );
    onDomainsChange(next);
  };

  const updateListeners = (next: ServiceListener[]) => {
    setListenerPorts((current) =>
      Object.fromEntries(
        next.map((listener) => {
          const key = serviceListenerKey(listener);
          return [key, current[key] ?? listener.targetPort];
        })
      )
    );
    onListenersChange(next);
  };

  return (
    <div>
      <ServiceConfiguration
        credentials={credentials}
        draft={configuration}
        embeddedRegistryHost={embeddedRegistryHost}
        onCredentialCreated={onCredentialCreated}
        onDraftChange={setConfiguration}
        projectID={projectID}
      />
      <ServiceVolumes
        mounts={volumeMounts}
        onMountsChange={setVolumeMounts}
        onVolumesChange={onVolumesChange}
        projectID={projectID}
        serviceID={serviceID}
        volumes={volumes}
      />

      <section className="grid border-b border-border lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="flex items-center gap-2 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            <Network className="size-3" /> Private network
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Other project resources can connect on any port the service listens
            on.
          </p>
        </div>
        <div className="flex items-center border-t border-border px-5 py-4 lg:border-t-0 lg:border-l">
          <div>
            <p className="text-[9px] text-muted-foreground">
              Internal hostname
            </p>
            <code className="mt-1 block text-[10px] text-foreground">
              {internalHostname}
            </code>
          </div>
        </div>
      </section>

      <ServiceDomains
        domains={domains}
        onChanged={updateDomains}
        onPortDraftChange={(hostname, port) =>
          setDomainPorts((current) => ({ ...current, [hostname]: port }))
        }
        portDrafts={domainPorts}
        projectID={projectID}
        serviceID={serviceID}
      />
      <ServiceListeners
        listeners={listeners}
        onChanged={updateListeners}
        onPortDraftChange={(key, port) =>
          setListenerPorts((current) => ({ ...current, [key]: port }))
        }
        portDrafts={listenerPorts}
        projectID={projectID}
        serviceID={serviceID}
      />

      <div className="flex items-center justify-end gap-3 border-b border-border px-5 py-3">
        {error || actionError ? (
          <p className="mr-auto text-[10px] text-destructive">
            {error ?? actionError}
          </p>
        ) : null}
        <Button
          disabled={busy || configuration.imageReference.trim() === ""}
          onClick={() => void save()}
          type="button"
        >
          <Save />
          {busy ? "Saving…" : "Save settings"}
        </Button>
      </div>

      <section className="grid border-b border-border lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="text-[9px] tracking-[0.13em] text-destructive uppercase">
            Delete service
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Permanently removes the service, deployment history, and owned
            volumes.
          </p>
        </div>
        <div className="border-t border-border px-5 py-4 lg:border-t-0 lg:border-l">
          {deleting ? (
            <div className="max-w-lg">
              <p className="text-[10px] leading-4 text-destructive">
                Type {service.name} to confirm deletion.
              </p>
              <div className="mt-3 flex gap-2">
                <Input
                  aria-label={`Confirm deletion of ${service.name}`}
                  autoComplete="off"
                  onChange={(event) => setConfirmation(event.target.value)}
                  value={confirmation}
                />
                <Button
                  disabled={busy || confirmation !== service.name}
                  onClick={() => void onDelete()}
                  type="button"
                  variant="destructive"
                >
                  Delete
                </Button>
                <Button
                  disabled={busy}
                  onClick={() => {
                    setDeleting(false);
                    setConfirmation("");
                  }}
                  type="button"
                  variant="ghost"
                >
                  Cancel
                </Button>
              </div>
            </div>
          ) : (
            <Button
              disabled={busy}
              onClick={() => setDeleting(true)}
              type="button"
              variant="destructive"
            >
              <Trash2 /> Delete service
            </Button>
          )}
        </div>
      </section>
    </div>
  );
};
