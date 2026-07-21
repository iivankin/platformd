import { Network, Server } from "lucide-react";

import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import { ServiceConfiguration } from "@/service-configuration";
import { ServiceDomains } from "@/service-domains";
import { ServiceListeners } from "@/service-listeners";
import { ServiceVolumes } from "@/service-volumes";

type ServiceDraft = Extract<PendingResourceCreation, { kind: "service" }>;

export const ServiceDraftSettings = ({
  draft,
  embeddedRegistryHost,
  internalHostname,
  onChange,
  projectID,
}: {
  draft: ServiceDraft;
  embeddedRegistryHost: string;
  internalHostname: string;
  onChange: (draft: ServiceDraft) => void;
  projectID: string;
}) => {
  const updateSettings = (settings: ServiceDraft["settings"]) =>
    onChange({ ...draft, settings });

  return (
    <div className="grid gap-3">
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="flex items-center gap-2 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            <Server className="size-3" /> Service
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Name used on the project canvas and private network.
          </p>
        </div>
        <div className="border-t border-border px-5 py-4 lg:border-t-0 lg:border-l">
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="service-draft-name"
          >
            Service name
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="service-draft-name"
              onChange={(event) =>
                onChange({
                  ...draft,
                  input: { ...draft.input, name: event.target.value },
                })
              }
              required
              spellCheck={false}
              value={draft.input.name}
            />
          </label>
        </div>
      </SectionCard>

      <ServiceConfiguration
        draft={draft.settings.configuration}
        embeddedRegistryHost={embeddedRegistryHost}
        httpDomainCount={draft.settings.domains.length}
        onDraftChange={(configuration) =>
          updateSettings({ ...draft.settings, configuration })
        }
      />

      <ServiceVolumes
        mounts={draft.settings.volumeMounts}
        onMountsChange={(volumeMounts) =>
          updateSettings({ ...draft.settings, volumeMounts })
        }
        onVolumesChange={(volumes) =>
          updateSettings({ ...draft.settings, volumes })
        }
        projectID={projectID}
        serviceID={draft.id}
        volumes={draft.settings.volumes}
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
        containerPorts={[]}
        domains={draft.settings.domains}
        onChanged={(domains) => updateSettings({ ...draft.settings, domains })}
      />
      <ServiceListeners
        containerPorts={[]}
        listeners={draft.settings.listeners}
        onChanged={(listeners) =>
          updateSettings({ ...draft.settings, listeners })
        }
      />

      <p className="border border-border bg-muted/15 px-5 py-4 text-[9px] text-muted-foreground">
        Draft changes stay local until Deploy.
      </p>
    </div>
  );
};
