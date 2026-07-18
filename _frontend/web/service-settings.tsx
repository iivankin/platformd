import { Network, Trash2 } from "lucide-react";
import { useState } from "react";

import type {
  ImageCredential,
  Service,
  ServiceDomain,
  ServiceListener,
  Volume,
} from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { ServiceConfiguration } from "@/service-configuration";
import { ServiceDomains } from "@/service-domains";
import { ServiceListeners } from "@/service-listeners";
import {
  createPendingServiceSettings,
  createServiceSettingsDraft,
} from "@/service-settings-model";
import type {
  PendingServiceSettings,
  ServiceSettingsDraft,
} from "@/service-settings-model";
import { ServiceVolumes } from "@/service-volumes";

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
  onDraftChange: (change?: PendingServiceSettings) => void;
  onVolumesChange: (volumes: Volume[]) => void;
  pendingChange?: PendingServiceSettings;
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
  onDraftChange,
  onVolumesChange,
  pendingChange,
  projectID,
  service,
  serviceID,
  volumes,
}: ServiceSettingsProperties) => {
  const [draft, setDraft] = useState<ServiceSettingsDraft>(
    () =>
      pendingChange?.draft ??
      createServiceSettingsDraft(service, domains, listeners, volumes)
  );
  const [deleting, setDeleting] = useState(false);
  const [confirmation, setConfirmation] = useState("");

  const updateDraft = (next: ServiceSettingsDraft) => {
    setDraft(next);
    onDraftChange(
      createPendingServiceSettings({
        current: pendingChange,
        domains,
        draft: next,
        listeners,
        service,
        volumes,
      })
    );
  };

  return (
    <div className="grid gap-3">
      <ServiceConfiguration
        credentials={credentials}
        draft={draft.configuration}
        embeddedRegistryHost={embeddedRegistryHost}
        onCredentialCreated={onCredentialCreated}
        onDraftChange={(configuration) =>
          updateDraft({ ...draft, configuration })
        }
        projectID={projectID}
      />
      <ServiceVolumes
        mounts={draft.volumeMounts}
        onMountsChange={(volumeMounts) =>
          updateDraft({ ...draft, volumeMounts })
        }
        onPersistedVolumesChange={onVolumesChange}
        onVolumesChange={(nextVolumes) =>
          updateDraft({ ...draft, volumes: nextVolumes })
        }
        projectID={projectID}
        serviceID={serviceID}
        volumes={draft.volumes}
      />

      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
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
      </SectionCard>

      <ServiceDomains
        disabled={busy}
        domains={draft.domains}
        onChanged={(nextDomains) =>
          updateDraft({ ...draft, domains: nextDomains })
        }
      />
      <ServiceListeners
        disabled={busy}
        listeners={draft.listeners}
        onChanged={(nextListeners) =>
          updateDraft({ ...draft, listeners: nextListeners })
        }
      />

      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
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
          {actionError ? (
            <p className="mb-3 text-[10px] text-destructive">{actionError}</p>
          ) : null}
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
      </SectionCard>
    </div>
  );
};
