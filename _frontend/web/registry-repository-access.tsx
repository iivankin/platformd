import { LockKeyhole } from "lucide-react";
import { useState } from "react";

import { setRegistryRepositoryPublicPull } from "@/api";
import type { RegistryRepository } from "@/api";
import { Button } from "@/components/ui/button";
import { RegistryCredentials } from "@/registry-credentials";

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : "Unable to update access";

export const RegistryRepositoryAccess = ({
  hostname,
  onChanged,
  repository,
}: {
  hostname: string;
  onChanged: () => void;
  repository: RegistryRepository;
}) => {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const togglePublicPull = async () => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await setRegistryRepositoryPublicPull(
        repository.id,
        !repository.publicPull
      );
      onChanged();
    } catch (updateError) {
      setError(errorText(updateError));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <section className="flex items-center gap-4 border-b border-border px-4 py-5">
        <span className="grid size-9 place-items-center bg-muted">
          <LockKeyhole className="size-4" />
        </span>
        <div>
          <p className="text-[10px] font-medium">Image downloads</p>
          <p className="mt-1 text-[9px] text-muted-foreground">
            {repository.publicPull
              ? "Anyone can download images. Uploads still require a credential."
              : "Downloads and uploads require a repository credential."}
          </p>
        </div>
        <Button
          className="ml-auto"
          disabled={busy}
          onClick={() => void togglePublicPull()}
          variant="outline"
        >
          {repository.publicPull
            ? "Require credentials"
            : "Allow public downloads"}
        </Button>
      </section>

      <RegistryCredentials hostname={hostname} repositoryID={repository.id} />

      {error ? (
        <p className="border-b border-destructive/30 px-4 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
};
