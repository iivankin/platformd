import { Cloud, DatabaseBackup, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { deleteBackupTarget, fetchBackupTarget, setBackupTarget } from "@/api";
import type { BackupTarget, SetBackupTargetInput } from "@/api";
import { BackupStorageForm } from "@/backup-storage-form";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const emptyInput: SetBackupTargetInput = {
  accessKeyId: "",
  bucket: "",
  endpoint: "",
  prefix: "",
  region: "",
  secretAccessKey: "",
};

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const completeTargetInput = (input: SetBackupTargetInput) =>
  Boolean(
    input.endpoint.trim() &&
    input.region.trim() &&
    input.bucket.trim() &&
    input.accessKeyId.trim() &&
    input.secretAccessKey
  );

export const BackupStoragePage = () => {
  const [target, setTarget] = useState<BackupTarget>({ configured: false });
  const [input, setInput] = useState<SetBackupTargetInput>(emptyInput);
  const [editing, setEditing] = useState(true);
  const [busy, setBusy] = useState("");
  const [deleteConfirmation, setDeleteConfirmation] = useState("");
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchBackupTarget(controller.signal);
        setTarget(loaded);
        setEditing(!loaded.configured);
        if (loaded.configured) {
          setInput({
            accessKeyId: loaded.accessKeyId ?? "",
            bucket: loaded.bucket ?? "",
            endpoint: loaded.endpoint ?? "",
            prefix: loaded.prefix ?? "",
            region: loaded.region ?? "",
            secretAccessKey: "",
          });
        }
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
    void load();
    return () => controller.abort();
  }, []);

  const updateInput = (field: keyof SetBackupTargetInput, value: string) => {
    setInput((current) => ({ ...current, [field]: value }));
  };

  const save = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || !completeTargetInput(input)) {
      return;
    }
    setBusy("save");
    setError(undefined);
    try {
      const updated = await setBackupTarget({
        ...input,
        accessKeyId: input.accessKeyId.trim(),
        bucket: input.bucket.trim(),
        endpoint: input.endpoint.trim(),
        prefix: input.prefix.trim(),
        region: input.region.trim(),
      });
      setTarget(updated);
      setInput((current) => ({ ...current, secretAccessKey: "" }));
      setEditing(false);
      setDeleteConfirmation("");
    } catch (saveError) {
      setError(errorText(saveError, "Unable to connect backup storage"));
    } finally {
      setBusy("");
    }
  };

  const remove = async () => {
    if (busy || !target.configured || deleteConfirmation !== target.bucket) {
      return;
    }
    setBusy("delete");
    setError(undefined);
    try {
      await deleteBackupTarget();
      setTarget({ configured: false });
      setInput(emptyInput);
      setEditing(true);
      setDeleteConfirmation("");
    } catch (deleteError) {
      setError(errorText(deleteError, "Unable to remove backup storage"));
    } finally {
      setBusy("");
    }
  };

  return (
    <div>
      <section className="flex min-h-16 items-center gap-4 border-b border-border px-5 py-3">
        <span className="grid size-9 place-items-center border border-border bg-muted/30">
          <DatabaseBackup className="size-4" />
        </span>
        <div>
          <h3 className="text-xs font-medium">Backup storage</h3>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Choose where encrypted backups are stored.
          </p>
        </div>
        <span
          className={cn(
            "ml-auto inline-flex items-center gap-1.5 border px-2 py-1 text-[9px]",
            target.configured
              ? "border-emerald-500/30 text-emerald-600"
              : "border-amber-500/30 text-amber-600"
          )}
        >
          <span
            className={cn(
              "size-1.5 rounded-full",
              target.configured ? "bg-emerald-500" : "bg-amber-500"
            )}
          />
          {target.configured ? "CONNECTED" : "NOT CONNECTED"}
        </span>
      </section>

      {target.configured && !editing ? (
        <section className="border-b border-border">
          <div className="flex flex-col gap-4 px-5 py-5 sm:flex-row sm:items-center">
            <Cloud className="size-5 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
                Connected bucket
              </p>
              <p className="mt-1 truncate text-xs font-medium">
                {target.bucket}
              </p>
            </div>
            <Button
              onClick={() => setEditing(true)}
              size="sm"
              variant="outline"
            >
              <RefreshCw />
              Change storage
            </Button>
          </div>
          <details className="border-t border-border">
            <summary className="cursor-pointer px-5 py-3 text-[10px] text-muted-foreground hover:text-foreground">
              Advanced connection details
            </summary>
            <dl className="grid border-t border-border bg-muted/15 md:grid-cols-2 xl:grid-cols-4">
              {[
                ["Endpoint", target.endpoint ?? ""],
                ["Region", target.region ?? ""],
                ["Path prefix", target.prefix || "Bucket root"],
                ["Access key", target.accessKeyId ?? ""],
              ].map(([label, value]) => (
                <div
                  className="min-w-0 border-b border-border px-5 py-4 md:border-r"
                  key={label}
                >
                  <dt className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                    {label}
                  </dt>
                  <dd
                    className="mt-2 truncate font-mono text-[10px]"
                    title={value}
                  >
                    {value}
                  </dd>
                </div>
              ))}
            </dl>
          </details>
        </section>
      ) : (
        <BackupStorageForm
          busy={busy === "save"}
          canSubmit={completeTargetInput(input)}
          configured={target.configured}
          input={input}
          onCancel={() => {
            setEditing(false);
            setInput((current) => ({
              ...current,
              secretAccessKey: "",
            }));
          }}
          onSubmit={save}
          onUpdate={updateInput}
        />
      )}

      {target.configured ? (
        <details className="border-b border-border">
          <summary className="cursor-pointer px-5 py-3 text-[10px] text-muted-foreground hover:text-destructive">
            Remove storage connection
          </summary>
          <section className="border-t border-destructive/25 bg-destructive/5 px-5 py-4">
            <h3 className="text-[10px] font-medium text-destructive">
              Remove backup storage
            </h3>
            <p className="mt-1 text-[9px] text-muted-foreground">
              Stored backups are not deleted. Type the bucket name to remove
              only this connection.
            </p>
            <div className="mt-3 flex max-w-xl gap-2">
              <Input
                onChange={(event) => setDeleteConfirmation(event.target.value)}
                placeholder={target.bucket}
                value={deleteConfirmation}
              />
              <Button
                disabled={Boolean(busy) || deleteConfirmation !== target.bucket}
                onClick={() => void remove()}
                variant="destructive"
              >
                <Trash2 />
                Remove
              </Button>
            </div>
          </section>
        </details>
      ) : null}

      {error ? (
        <p
          aria-live="polite"
          className="border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </div>
  );
};
