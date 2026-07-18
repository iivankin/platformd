import { ArrowLeft, DatabaseBackup } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router";

import { createBackupTarget } from "@/api";
import type { SetBackupTargetInput } from "@/api";
import { BackupStorageForm } from "@/backup-storage-form";
import {
  completeBackupTargetInput,
  emptyBackupTargetInput,
  normalizeBackupTargetInput,
} from "@/backup-storage-input";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : "Unable to connect backup storage";

export const BackupStorageCreatePage = () => {
  const navigate = useNavigate();
  const [input, setInput] = useState<SetBackupTargetInput>(
    emptyBackupTargetInput
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || !completeBackupTargetInput(input)) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await createBackupTarget(normalizeBackupTargetInput(input));
      navigate("/backups/storage");
    } catch (saveError) {
      setError(errorText(saveError));
      setBusy(false);
    }
  };

  return (
    <PageStack>
      <SectionCard className="flex min-h-16 items-center gap-4 px-5 py-3">
        <Button
          aria-label="Back to backup storage"
          onClick={() => navigate("/backups/storage")}
          size="icon"
          variant="outline"
        >
          <ArrowLeft />
        </Button>
        <span className="grid size-9 place-items-center border border-border bg-muted/30">
          <DatabaseBackup className="size-4" />
        </span>
        <div>
          <h3 className="text-xs font-medium">Connect backup storage</h3>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Add one independent S3-compatible destination.
          </p>
        </div>
      </SectionCard>

      <BackupStorageForm
        busy={busy}
        canSubmit={completeBackupTargetInput(input)}
        configured={false}
        input={input}
        onCancel={() => navigate("/backups/storage")}
        onSubmit={submit}
        onUpdate={(field, value) =>
          setInput((current) => ({ ...current, [field]: value }))
        }
      />

      {error ? (
        <SectionCard className="bg-destructive/5 px-5 py-3 text-[10px] text-destructive ring-destructive/30">
          {error}
        </SectionCard>
      ) : null}
    </PageStack>
  );
};
