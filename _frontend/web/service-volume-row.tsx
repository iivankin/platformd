import { DatabaseBackup, Link, Trash2, Unlink } from "lucide-react";
import { useState } from "react";

import { deleteVolume } from "@/api";
import type { Service } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ResourceBackupPanel } from "@/resource-backup-panel";
import type { ServiceVolumeDraft } from "@/service-settings-model";

interface ServiceVolumeRowProperties {
  item: ServiceVolumeDraft;
  mount?: Service["volumeMounts"][number];
  onDeleted: () => void;
  onMountChange: (containerPath?: string) => void;
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
  const [showBackups, setShowBackups] = useState(false);

  const updateMount = (containerPath?: string) => {
    setError(undefined);
    onMountChange(containerPath?.trim());
    setMountPath("");
  };

  const remove = async () => {
    if (item.pendingCreation) {
      onDeleted();
      return;
    }
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

  const mountDescription = () => {
    if (item.pendingCreation) {
      return mount
        ? `Will mount at ${mount.containerPath}`
        : "Will be created on deploy";
    }
    return mount?.containerPath ?? "Not mounted";
  };

  const volumeAction = () => {
    if (item.pendingCreation) {
      return (
        <Button
          aria-label={`Remove pending ${item.name}`}
          disabled={busy}
          onClick={() => void remove()}
          size="icon"
          variant="ghost"
        >
          <Trash2 />
        </Button>
      );
    }
    if (mount) {
      return (
        <Button
          disabled={busy}
          onClick={() => updateMount()}
          size="sm"
          variant="outline"
        >
          <Unlink />
          Unmount
        </Button>
      );
    }
    return (
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
    );
  };

  return (
    <div className="border-b border-border last:border-b-0">
      <div className="px-4 py-3">
        <div className="flex items-start gap-3">
          <div className="min-w-0 flex-1">
            <p className="truncate text-[10px] font-medium">{item.name}</p>
            <p className="mt-1 font-mono text-[9px] text-muted-foreground">
              {mountDescription()}
            </p>
          </div>
          {volumeAction()}
          {item.pendingCreation ? null : (
            <Button
              aria-pressed={showBackups}
              onClick={() => setShowBackups((visible) => !visible)}
              size="sm"
              variant="outline"
            >
              <DatabaseBackup /> Backups
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
              onClick={() => updateMount(mountPath)}
              size="sm"
              variant="outline"
            >
              <Link />
              Mount
            </Button>
          </div>
        )}
        {deleting && !item.pendingCreation ? (
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
      {showBackups && !item.pendingCreation ? (
        <div className="border-t border-border">
          <ResourceBackupPanel resourceID={item.id} resourceKind="volume" />
        </div>
      ) : null}
    </div>
  );
};
