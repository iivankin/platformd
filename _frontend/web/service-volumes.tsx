import { HardDrive, Plus } from "lucide-react";
import { useState } from "react";

import type { Service, Volume } from "@/api";
import { Button } from "@/components/ui/button";
import { ServiceVolumeCreateForm } from "@/service-volume-create-form";
import { ServiceVolumeRow } from "@/service-volume-row";

interface ServiceVolumesProperties {
  onMountsChange: (mounts: Service["volumeMounts"]) => Promise<boolean>;
  onVolumesChange: (volumes: Volume[]) => void;
  projectID: string;
  service: Service;
  serviceID: string;
  volumes: Volume[];
}

export const ServiceVolumes = ({
  onMountsChange,
  onVolumesChange,
  projectID,
  service,
  serviceID,
  volumes,
}: ServiceVolumesProperties) => {
  const [creating, setCreating] = useState(false);

  return (
    <section className="border-b border-border">
      <div className="flex items-center gap-2 px-4 py-3">
        <HardDrive className="size-3.5 text-muted-foreground" />
        <div>
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Writable volumes
          </h3>
          <p className="mt-0.5 text-[9px] text-muted-foreground">
            Owned by this service · immutable UID/GID
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

      <p className="border-t border-border px-4 py-2 text-[9px] leading-4 text-muted-foreground">
        Mount changes trigger a stop-first redeploy. If the candidate changes
        writable data and then fails, restarting the old container does not roll
        that data back.
      </p>

      {creating ? (
        <ServiceVolumeCreateForm
          onCancel={() => setCreating(false)}
          onCreated={(created) => {
            onVolumesChange([...volumes, created]);
            setCreating(false);
          }}
          projectID={projectID}
          serviceID={serviceID}
        />
      ) : null}

      {volumes.length === 0 ? (
        <p className="border-t border-border px-4 py-5 text-[10px] text-muted-foreground">
          No persistent volumes. Image-declared VOLUME paths remain ephemeral.
        </p>
      ) : (
        <div className="border-t border-border">
          {volumes.map((item) => (
            <ServiceVolumeRow
              item={item}
              key={item.id}
              mount={service.volumeMounts.find(
                (candidate) => candidate.volumeId === item.id
              )}
              onDeleted={() =>
                onVolumesChange(
                  volumes.filter((volume) => volume.id !== item.id)
                )
              }
              onMountChange={(containerPath) => {
                const mounts = service.volumeMounts.filter(
                  (mount) => mount.volumeId !== item.id
                );
                if (containerPath) {
                  mounts.push({ containerPath, volumeId: item.id });
                }
                return onMountsChange(mounts);
              }}
              projectID={projectID}
              serviceID={serviceID}
            />
          ))}
        </div>
      )}
    </section>
  );
};
