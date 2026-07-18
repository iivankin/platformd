import { DatabaseBackup, Plus, ShieldCheck } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router";

import {
  deleteBackupTarget,
  fetchBackupTargets,
  setControlBackupTarget,
  updateBackupTarget,
} from "@/api";
import type { BackupTarget, BackupTargets, SetBackupTargetInput } from "@/api";
import { BackupStorageForm } from "@/backup-storage-form";
import {
  completeBackupTargetInput,
  emptyBackupTargetInput,
  normalizeBackupTargetInput,
} from "@/backup-storage-input";
import {
  BackupStorageDeleteConfirmation,
  BackupStorageLocations,
} from "@/backup-storage-locations";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const noControlTarget = "__no-control-target__";

export const BackupStoragePage = () => {
  const navigate = useNavigate();
  const [data, setData] = useState<BackupTargets>({
    controlTargetId: "",
    targets: [],
  });
  const [input, setInput] = useState<SetBackupTargetInput>(
    emptyBackupTargetInput
  );
  const [editing, setEditing] = useState<BackupTarget>();
  const [busy, setBusy] = useState("");
  const [deleting, setDeleting] = useState<BackupTarget>();
  const [error, setError] = useState<string>();
  const [loaded, setLoaded] = useState(false);

  const load = async (signal?: AbortSignal) => {
    try {
      const loadedTargets = await fetchBackupTargets(signal);
      setData(loadedTargets);
    } finally {
      if (!signal?.aborted) {
        setLoaded(true);
      }
    }
  };

  useEffect(() => {
    const controller = new AbortController();
    const loadInitial = async () => {
      try {
        await load(controller.signal);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load backup storage"));
        }
      }
    };
    void loadInitial();
    return () => {
      controller.abort();
    };
  }, []);

  const startEdit = (target: BackupTarget) => {
    setDeleting(undefined);
    setEditing(target);
    setInput({
      accessKeyId: target.accessKeyId,
      bucket: target.bucket,
      endpoint: target.endpoint,
      name: target.name,
      prefix: target.prefix,
      region: target.region,
      secretAccessKey: "",
    });
  };

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!editing || busy || !completeBackupTargetInput(input)) {
      return;
    }
    setBusy("save");
    setError(undefined);
    try {
      await updateBackupTarget(editing.id, normalizeBackupTargetInput(input));
      setEditing(undefined);
      setInput(emptyBackupTargetInput);
      await load();
    } catch (saveError) {
      setError(errorText(saveError, "Unable to connect backup storage"));
    } finally {
      setBusy("");
    }
  };

  const selectControlTarget = async (targetID: string) => {
    if (busy) {
      return;
    }
    setBusy("control");
    setError(undefined);
    try {
      const selected = await setControlBackupTarget(targetID);
      setData((current) => ({ ...current, controlTargetId: selected }));
    } catch (selectionError) {
      setError(
        errorText(selectionError, "Unable to update disaster recovery storage")
      );
    } finally {
      setBusy("");
    }
  };

  const remove = async () => {
    if (!deleting || busy) {
      return;
    }
    setBusy("delete");
    setError(undefined);
    try {
      await deleteBackupTarget(deleting.id);
      setDeleting(undefined);
      await load();
    } catch (deleteError) {
      setError(errorText(deleteError, "Unable to remove backup storage"));
    } finally {
      setBusy("");
    }
  };

  return (
    <PageStack>
      <SectionCard className="flex min-h-16 items-center gap-4 px-5 py-3">
        <span className="grid size-9 place-items-center border border-border bg-muted/30">
          <DatabaseBackup className="size-4" />
        </span>
        <div>
          <h3 className="text-xs font-medium">Backup storage</h3>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Add independent S3-compatible locations and choose one per backup.
          </p>
        </div>
        <Button
          className="ml-auto"
          onClick={() => navigate("/backups/storage/new")}
          size="sm"
        >
          <Plus />
          Add storage
        </Button>
      </SectionCard>

      {editing ? (
        <BackupStorageForm
          busy={busy === "save"}
          canSubmit={completeBackupTargetInput(input)}
          configured
          input={input}
          onCancel={() => {
            setEditing(undefined);
            setInput(emptyBackupTargetInput);
          }}
          onSubmit={save}
          onUpdate={(field, value) =>
            setInput((current) => ({ ...current, [field]: value }))
          }
        />
      ) : null}

      <SectionCard className="grid lg:grid-cols-[14rem_1fr]">
        <div className="px-5 py-4">
          <div className="flex items-center gap-2 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            <ShieldCheck className="size-3.5" />
            Disaster recovery
          </div>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Control snapshots are read from this storage when rebuilding a VPS.
          </p>
        </div>
        <div className="border-t border-border px-5 py-4 lg:border-t-0 lg:border-l">
          <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Control backup destination
          </span>
          <Select
            disabled={busy === "control" || data.targets.length === 0}
            items={[
              { label: "Not configured", value: noControlTarget },
              ...data.targets.map((target) => ({
                label: `${target.name} · ${target.bucket}`,
                value: target.id,
              })),
            ]}
            onValueChange={(value) =>
              void selectControlTarget(
                value === noControlTarget ? "" : String(value)
              )
            }
            value={data.controlTargetId || noControlTarget}
          >
            <SelectTrigger className="mt-2 h-9 w-full text-[10px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="start">
              <SelectItem value={noControlTarget}>Not configured</SelectItem>
              {data.targets.map((target) => (
                <SelectItem key={target.id} value={target.id}>
                  {target.name} · {target.bucket}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </SectionCard>

      <BackupStorageLocations
        controlTargetID={data.controlTargetId}
        loaded={loaded}
        onDelete={setDeleting}
        onEdit={startEdit}
        targets={data.targets}
      />

      {deleting ? (
        <BackupStorageDeleteConfirmation
          busy={Boolean(busy)}
          key={deleting.id}
          onCancel={() => setDeleting(undefined)}
          onConfirm={() => void remove()}
          target={deleting}
        />
      ) : null}

      {error ? (
        <SectionCard className="bg-destructive/5 px-5 py-3 text-[10px] text-destructive ring-destructive/30">
          {error}
        </SectionCard>
      ) : null}
    </PageStack>
  );
};
