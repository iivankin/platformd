import { Cloud, LoaderCircle, Pencil, Trash2 } from "lucide-react";
import { useState } from "react";

import type { BackupTarget } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

export const BackupStorageLocations = ({
  controlTargetID,
  loaded,
  onDelete,
  onEdit,
  targets,
}: {
  controlTargetID: string;
  loaded: boolean;
  onDelete: (target: BackupTarget) => void;
  onEdit: (target: BackupTarget) => void;
  targets: BackupTarget[];
}) => (
  <SectionCard>
    <div className="flex items-center gap-2 border-b border-border px-5 py-3">
      <Cloud className="size-3.5 text-muted-foreground" />
      <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
        Storage locations
      </h3>
      <span className="ml-auto text-[9px] text-muted-foreground">
        {targets.length}
      </span>
    </div>
    {loaded ? null : (
      <p className="flex items-center gap-2 px-5 py-5 text-[10px] text-muted-foreground">
        <LoaderCircle className="size-3 animate-spin" /> Loading storage
      </p>
    )}
    {loaded && targets.length === 0 ? (
      <p className="px-5 py-5 text-[10px] text-muted-foreground">
        No backup storage connected.
      </p>
    ) : null}
    {loaded
      ? targets.map((target) => (
          <div
            className="grid items-center border-b border-border px-5 py-3 last:border-b-0 md:grid-cols-[minmax(10rem,0.8fr)_minmax(14rem,1.4fr)_auto]"
            key={target.id}
          >
            <div className="min-w-0">
              <p className="truncate text-[10px] font-medium">{target.name}</p>
              {target.id === controlTargetID ? (
                <p className="mt-1 text-[8px] text-emerald-600">
                  DISASTER RECOVERY
                </p>
              ) : null}
            </div>
            <div className="min-w-0 py-2 md:py-0">
              <p className="truncate font-mono text-[9px]">{target.bucket}</p>
              <p className="mt-1 truncate text-[9px] text-muted-foreground">
                {target.endpoint}
                {target.prefix ? ` / ${target.prefix}` : ""}
              </p>
            </div>
            <div className="flex justify-end gap-1">
              <Button
                aria-label={`Edit ${target.name}`}
                onClick={() => onEdit(target)}
                size="icon"
                variant="ghost"
              >
                <Pencil />
              </Button>
              <Button
                aria-label={`Remove ${target.name}`}
                onClick={() => onDelete(target)}
                size="icon"
                variant="ghost"
              >
                <Trash2 />
              </Button>
            </div>
          </div>
        ))
      : null}
  </SectionCard>
);

export const BackupStorageDeleteConfirmation = ({
  busy,
  onCancel,
  onConfirm,
  target,
}: {
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
  target: BackupTarget;
}) => {
  const [confirmation, setConfirmation] = useState("");
  return (
    <SectionCard className="bg-destructive/5 px-5 py-4 ring-destructive/25">
      <h3 className="text-[10px] font-medium text-destructive">
        Remove {target.name}
      </h3>
      <p className="mt-1 text-[9px] text-muted-foreground">
        Stored objects stay in the bucket. Policies and disaster recovery must
        use another storage first.
      </p>
      <div className="mt-3 flex max-w-xl gap-2">
        <Input
          aria-label={`Confirm removal of ${target.name}`}
          onChange={(event) => setConfirmation(event.target.value)}
          placeholder={target.name}
          value={confirmation}
        />
        <Button
          disabled={busy || confirmation !== target.name}
          onClick={onConfirm}
          variant="destructive"
        >
          <Trash2 /> Remove
        </Button>
        <Button disabled={busy} onClick={onCancel} variant="ghost">
          Cancel
        </Button>
      </div>
    </SectionCard>
  );
};
