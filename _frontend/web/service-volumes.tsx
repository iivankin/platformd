import { HardDrive, Plus } from "lucide-react";
import { useState } from "react";

import type { Service, Volume } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import type { ServiceVolumeDraft } from "@/service-settings-model";
import { ServiceVolumeCreateForm } from "@/service-volume-create-form";
import { ServiceVolumeRow } from "@/service-volume-row";

interface ServiceVolumesProperties {
  mounts: Service["volumeMounts"];
  onMountsChange: (mounts: Service["volumeMounts"]) => void;
  onPersistedVolumesChange?: (volumes: Volume[]) => void;
  onVolumesChange: (volumes: ServiceVolumeDraft[]) => void;
  projectID: string;
  serviceID: string;
  volumes: ServiceVolumeDraft[];
}

export const ServiceVolumes = ({
  mounts,
  onMountsChange,
  onPersistedVolumesChange,
  onVolumesChange,
  projectID,
  serviceID,
  volumes,
}: ServiceVolumesProperties) => {
  const [creating, setCreating] = useState(false);

  return (
    <SectionCard>
      <div className="flex items-center gap-2 px-4 py-3">
        <HardDrive className="size-3.5 text-muted-foreground" />
        <div>
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Writable volumes
          </h3>
          <p className="mt-0.5 text-[9px] text-muted-foreground">
            Persistent data mounted into this service.
          </p>
        </div>
        <Button
          className="ml-auto"
          onClick={() => setCreating((value) => !value)}
          size="sm"
          variant="outline"
        >
          <Plus />
          Add
        </Button>
      </div>

      {creating ? (
        <ServiceVolumeCreateForm
          existingNames={volumes.map((volume) => volume.name)}
          onCancel={() => setCreating(false)}
          onCreated={(created) => {
            onVolumesChange([
              ...volumes,
              {
                ...created,
                createdAt: Date.now(),
                id: `pending-volume:${crypto.randomUUID()}`,
                pendingCreation: true,
                projectId: projectID,
                serviceId: serviceID,
              },
            ]);
            setCreating(false);
          }}
        />
      ) : null}

      {volumes.length === 0 ? (
        <p className="border-t border-border px-4 py-5 text-[10px] text-muted-foreground">
          No persistent volumes.
        </p>
      ) : (
        <div className="border-t border-border">
          {volumes.map((item) => (
            <ServiceVolumeRow
              item={item}
              key={item.id}
              mount={mounts.find((candidate) => candidate.volumeId === item.id)}
              onDeleted={() => {
                const nextVolumes = volumes.filter(
                  (volume) => volume.id !== item.id
                );
                onVolumesChange(nextVolumes);
                if (!item.pendingCreation) {
                  onPersistedVolumesChange?.(
                    nextVolumes.filter((volume) => !volume.pendingCreation)
                  );
                }
              }}
              onMountChange={(containerPath) => {
                const nextMounts = mounts.filter(
                  (mount) => mount.volumeId !== item.id
                );
                if (containerPath) {
                  nextMounts.push({ containerPath, volumeId: item.id });
                }
                onMountsChange(nextMounts);
              }}
              projectID={projectID}
              serviceID={serviceID}
            />
          ))}
        </div>
      )}
    </SectionCard>
  );
};
