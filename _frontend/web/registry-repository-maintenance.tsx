import { Trash2 } from "lucide-react";
import { useState } from "react";

import { deleteRegistryRepository } from "@/api";
import type { RegistryRepository } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RegistryCleanup } from "@/registry-cleanup";

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : "Unable to delete repository";

export const RegistryRepositoryMaintenance = ({
  onDeleted,
  onChanged,
  repository,
}: {
  onDeleted: (repositoryID: string) => void;
  onChanged: () => void;
  repository: RegistryRepository;
}) => {
  const [deleteName, setDeleteName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const removeRepository = async () => {
    if (deleteName !== repository.name || busy) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await deleteRegistryRepository(repository.id, deleteName);
      onDeleted(repository.id);
    } catch (deleteError) {
      setError(errorText(deleteError));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <section className="border-b border-border bg-muted/15 px-5 py-3">
        <h3 className="text-[10px] font-medium">Repository maintenance</h3>
        <p className="mt-1 text-[9px] text-muted-foreground">
          Clean unused image data or permanently remove this repository.
        </p>
      </section>
      <RegistryCleanup onChanged={onChanged} repositoryID={repository.id} />

      <details className="border-b border-border">
        <summary className="cursor-pointer px-4 py-3 text-[10px] text-muted-foreground hover:text-destructive">
          Delete repository
        </summary>
        <section className="border-t border-destructive/25 bg-destructive/5 px-4 py-4">
          <p className="text-[10px] font-medium text-destructive">
            Permanently delete {repository.name}
          </p>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            Images and repository credentials will be removed. Type the exact
            repository name to continue.
          </p>
          <div className="mt-3 flex max-w-xl gap-2">
            <Input
              onChange={(event) => setDeleteName(event.target.value)}
              placeholder={repository.name}
              value={deleteName}
            />
            <Button
              disabled={deleteName !== repository.name || busy}
              onClick={() => void removeRepository()}
              variant="destructive"
            >
              <Trash2 /> Delete repository
            </Button>
          </div>
        </section>
      </details>

      {error ? (
        <p className="border-b border-destructive/30 px-4 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
};
