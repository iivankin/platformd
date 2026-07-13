import {
  Check,
  Cloud,
  DatabaseBackup,
  LoaderCircle,
  RefreshCw,
  ShieldCheck,
  Trash2,
} from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { deleteBackupTarget, fetchBackupTarget, setBackupTarget } from "@/api";
import type { BackupTarget, SetBackupTargetInput } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";
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

const canDeleteTarget = (
  busy: string,
  target: BackupTarget,
  confirmation: string
) => !busy && target.configured && confirmation === target.bucket;

export const BackupsPage = () => {
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
          setError(errorText(loadError, "Unable to load remote backup target"));
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
      setError(
        errorText(saveError, "Remote target failed its capability probe")
      );
    } finally {
      setBusy("");
    }
  };

  const remove = async () => {
    if (!canDeleteTarget(busy, target, deleteConfirmation)) {
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
      setError(errorText(deleteError, "Unable to delete remote target"));
    } finally {
      setBusy("");
    }
  };

  return (
    <div className="enter-row min-h-full">
      <section className="flex min-h-16 items-center gap-4 border-b border-border px-5 py-3">
        <span className="grid size-9 place-items-center border border-border bg-muted/30">
          <DatabaseBackup className="size-4" />
        </span>
        <div>
          <h1 className="text-xs font-medium">Remote backups</h1>
          <p className="mt-1 text-[10px] text-muted-foreground">
            One offsite S3-compatible target · encrypted independent generations
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
          {target.configured ? "TARGET READY" : "TARGET REQUIRED"}
        </span>
      </section>

      <section className="border-b border-border">
        <div className="grid border-b border-border bg-muted/20 md:grid-cols-[190px_repeat(5,minmax(0,1fr))]">
          <div className="flex items-center gap-2 px-5 py-3 text-[9px] font-medium">
            <ShieldCheck className="size-3.5 text-muted-foreground" />
            Capability probe
          </div>
          {[
            "PUT random",
            "HEAD size",
            "GET compare",
            "LIST visible",
            "DELETE",
          ].map((step, index) => (
            <div
              className="flex items-center gap-2 border-t border-border px-3 py-3 text-[9px] text-muted-foreground md:border-t-0 md:border-l"
              key={step}
            >
              <span className="font-mono text-[8px]">
                {String(index + 1).padStart(2, "0")}
              </span>
              {busy === "save" ? (
                <LoaderCircle className="size-3 animate-spin" />
              ) : (
                <Check className="size-3" />
              )}
              {step}
            </div>
          ))}
        </div>

        {target.configured && !editing ? (
          <>
            <div className="grid md:grid-cols-[minmax(14rem,1.4fr)_minmax(9rem,0.7fr)_minmax(10rem,0.8fr)_minmax(10rem,1fr)_auto]">
              {[
                ["Endpoint", target.endpoint ?? ""],
                ["Region", target.region ?? ""],
                ["Bucket", target.bucket ?? ""],
                ["Prefix", target.prefix || "bucket root"],
              ].map(([label, value]) => (
                <div
                  className="min-w-0 border-b border-border px-5 py-4 md:border-r"
                  key={label}
                >
                  <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                    {label}
                  </p>
                  <p
                    className="mt-2 truncate font-mono text-[10px]"
                    title={value}
                  >
                    {value}
                  </p>
                </div>
              ))}
              <div className="flex items-center gap-2 border-b border-border px-4 py-3">
                <Button
                  onClick={() => setEditing(true)}
                  size="sm"
                  variant="outline"
                >
                  <RefreshCw />
                  Replace
                </Button>
              </div>
            </div>
            <div className="px-5 py-3 text-[9px] text-muted-foreground">
              Access key <span className="font-mono">
                {target.accessKeyId}
              </span>{" "}
              · secret is encrypted and never returned
            </div>
          </>
        ) : (
          <form onSubmit={save}>
            <div className="grid md:grid-cols-2 xl:grid-cols-3">
              <div className="border-b border-border px-5 pt-4 md:border-r">
                <FormField label="HTTPS endpoint" name="backup-endpoint">
                  <Input
                    autoComplete="url"
                    id="backup-endpoint"
                    onChange={(event) =>
                      updateInput("endpoint", event.target.value)
                    }
                    placeholder="https://s3.example.com"
                    value={input.endpoint}
                  />
                </FormField>
              </div>
              <div className="border-b border-border px-5 pt-4 xl:border-r">
                <FormField label="Region" name="backup-region">
                  <Input
                    id="backup-region"
                    onChange={(event) =>
                      updateInput("region", event.target.value)
                    }
                    placeholder="eu-central-003"
                    value={input.region}
                  />
                </FormField>
              </div>
              <div className="border-b border-border px-5 pt-4 md:border-r xl:border-r-0">
                <FormField label="Bucket" name="backup-bucket">
                  <Input
                    id="backup-bucket"
                    onChange={(event) =>
                      updateInput("bucket", event.target.value)
                    }
                    placeholder="platformd-backups"
                    value={input.bucket}
                  />
                </FormField>
              </div>
              <div className="border-b border-border px-5 pt-4 xl:border-r">
                <FormField label="Prefix (optional)" name="backup-prefix">
                  <Input
                    id="backup-prefix"
                    onChange={(event) =>
                      updateInput("prefix", event.target.value)
                    }
                    placeholder="installation-a"
                    value={input.prefix}
                  />
                </FormField>
              </div>
              <div className="border-b border-border px-5 pt-4 md:border-r">
                <FormField label="Access key ID" name="backup-access-key">
                  <Input
                    autoComplete="off"
                    id="backup-access-key"
                    onChange={(event) =>
                      updateInput("accessKeyId", event.target.value)
                    }
                    value={input.accessKeyId}
                  />
                </FormField>
              </div>
              <div className="border-b border-border px-5 pt-4">
                <FormField label="Secret access key" name="backup-secret-key">
                  <Input
                    autoComplete="new-password"
                    id="backup-secret-key"
                    onChange={(event) =>
                      updateInput("secretAccessKey", event.target.value)
                    }
                    placeholder={
                      target.configured
                        ? "Required again when replacing"
                        : "Stored encrypted"
                    }
                    type="password"
                    value={input.secretAccessKey}
                  />
                </FormField>
              </div>
            </div>
            <div className="flex items-center gap-2 px-5 py-3">
              <Cloud className="size-3.5 text-muted-foreground" />
              <p className="text-[9px] text-muted-foreground">
                Nothing is saved until all five remote operations succeed over
                verified TLS.
              </p>
              {target.configured ? (
                <Button
                  className="ml-auto"
                  onClick={() => {
                    setEditing(false);
                    setInput((current) => ({
                      ...current,
                      secretAccessKey: "",
                    }));
                  }}
                  size="sm"
                  type="button"
                  variant="ghost"
                >
                  Cancel
                </Button>
              ) : null}
              <Button
                className={target.configured ? "" : "ml-auto"}
                disabled={Boolean(busy) || !completeTargetInput(input)}
                size="sm"
                type="submit"
              >
                {busy === "save" ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <ShieldCheck />
                )}
                Probe and {target.configured ? "replace" : "save"}
              </Button>
            </div>
          </form>
        )}
      </section>

      <section className="border-b border-border">
        <div className="flex items-center gap-3 px-5 py-4">
          <div>
            <h2 className="text-[10px] font-medium">Resource policies</h2>
            <p className="mt-1 text-[9px] text-muted-foreground">
              Schedule, retention, generations, and restore belong to each exact
              PostgreSQL, Redis, Registry, or Object Store resource.
            </p>
          </div>
          <span className="ml-auto font-mono text-[9px] text-muted-foreground">
            UTC · 5-field cron · retention 1–100
          </span>
        </div>
      </section>

      {target.configured ? (
        <section className="border-b border-destructive/25 bg-destructive/5 px-5 py-4">
          <h2 className="text-[10px] font-medium text-destructive">
            Remove remote target
          </h2>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Existing remote objects remain untouched. Type the exact bucket name
            to remove only the platformd connection.
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
              Remove target
            </Button>
          </div>
        </section>
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
