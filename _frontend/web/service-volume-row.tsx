import { Link, Trash2, Unlink } from "lucide-react";
import { useState } from "react";

import { deleteVolume } from "@/api";
import type { Service, Volume } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface ServiceVolumeRowProperties {
  item: Volume;
  mount?: Service["volumeMounts"][number];
  onDeleted: () => void;
  onMountChange: (containerPath?: string) => Promise<boolean>;
  projectID: string;
  serviceID: string;
}

export const ServiceVolumeRow = ({
  item,
  mount,
  onDeleted,
  onMountChange,
  projectID,
  serviceID,
}: ServiceVolumeRowProperties) => {
  const [mountPath, setMountPath] = useState("");
  const [deleting, setDeleting] = useState(false);
  const [confirmation, setConfirmation] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const updateMount = async (containerPath?: string) => {
    setBusy(true);
    setError(undefined);
    try {
      if (await onMountChange(containerPath?.trim())) {
        setMountPath("");
      }
    } catch (mountError) {
      setError(
        mountError instanceof Error
          ? mountError.message
          : "Unable to update volume mount"
      );
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await deleteVolume(projectID, serviceID, item.id);
      onDeleted();
    } catch (deleteError) {
      setError(
        deleteError instanceof Error
          ? deleteError.message
          : "Unable to delete volume"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-b border-border px-4 py-3 last:border-b-0">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <p className="truncate text-[10px] font-medium">{item.name}</p>
          <p className="mt-1 font-mono text-[9px] text-muted-foreground">
            {item.ownerUid}:{item.ownerGid}
            {mount ? ` → ${mount.containerPath}` : " · unmounted"}
          </p>
        </div>
        {mount ? (
          <Button
            disabled={busy}
            onClick={() => void updateMount()}
            size="sm"
            variant="outline"
          >
            <Unlink />
            Unmount
          </Button>
        ) : (
          <Button
            aria-label={`Delete ${item.name}`}
            disabled={busy}
            onClick={() => {
              setDeleting(true);
              setConfirmation("");
            }}
            size="icon"
            variant="ghost"
          >
            <Trash2 />
          </Button>
        )}
      </div>
      {mount ? null : (
        <div className="mt-3 flex gap-2">
          <Input
            aria-label={`Mount path for ${item.name}`}
            className="font-mono text-[10px]"
            onChange={(event) => setMountPath(event.target.value)}
            placeholder="/data"
            value={mountPath}
          />
          <Button
            disabled={busy || !mountPath.trim()}
            onClick={() => void updateMount(mountPath)}
            size="sm"
            variant="outline"
          >
            <Link />
            Mount
          </Button>
        </div>
      )}
      {deleting ? (
        <div className="mt-3 border-t border-destructive/25 pt-3">
          <p className="text-[9px] leading-4 text-destructive">
            This permanently deletes all volume data. Type {item.name} to
            confirm.
          </p>
          <div className="mt-2 flex gap-2">
            <Input
              aria-label={`Confirm deletion of ${item.name}`}
              onChange={(event) => setConfirmation(event.target.value)}
              value={confirmation}
            />
            <Button
              disabled={busy || confirmation !== item.name}
              onClick={() => void remove()}
              size="sm"
              variant="destructive"
            >
              Delete
            </Button>
            <Button
              disabled={busy}
              onClick={() => setDeleting(false)}
              size="sm"
              variant="ghost"
            >
              Cancel
            </Button>
          </div>
        </div>
      ) : null}
      {error ? (
        <p aria-live="polite" className="mt-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
};
