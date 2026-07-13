import { Eraser, RefreshCw } from "lucide-react";
import { useState } from "react";

import { cleanupRegistryRepository } from "@/api";
import type { RegistryCleanup as CleanupResult } from "@/api";
import { Button } from "@/components/ui/button";
import { formatRegistryBytes } from "@/registry-repository-list";

export const RegistryCleanup = ({
  onChanged,
  repositoryID,
}: {
  onChanged: () => void;
  repositoryID: string;
}) => {
  const [preview, setPreview] = useState<CleanupResult>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const run = async (dryRun: boolean) => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const result = await cleanupRegistryRepository(repositoryID, dryRun);
      setPreview(result);
      if (result.deleted) {
        onChanged();
      }
    } catch (cleanupError) {
      setError(
        cleanupError instanceof Error
          ? cleanupError.message
          : "Registry cleanup failed"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="border-b border-border">
      <div className="flex items-center gap-2 px-4 py-3">
        <Eraser className="size-3.5 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <p className="text-[10px] font-medium">Repository cleanup</p>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Finds unreferenced repository-local blobs after the 24-hour safety
            grace.
          </p>
        </div>
        <Button
          disabled={busy}
          onClick={() => void run(true)}
          size="sm"
          variant="outline"
        >
          <RefreshCw />
          Dry run
        </Button>
        <Button
          disabled={
            busy || !preview || preview.deleted || preview.blobCount === 0
          }
          onClick={() => void run(false)}
          size="sm"
          variant="destructive"
        >
          Delete {preview?.blobCount ?? 0} blobs
        </Button>
      </div>
      {preview ? (
        <div className="border-t border-border px-4 py-2 text-[9px] text-muted-foreground">
          {preview.deleted ? "Deleted" : "Candidate"}:{" "}
          {preview.blobCount.toLocaleString()} blobs ·{" "}
          {formatRegistryBytes(preview.bytes)}
          {preview.previewTruncated ? " · digest preview truncated" : ""}
        </div>
      ) : null}
      {error ? (
        <p className="border-t border-destructive/30 px-4 py-2 text-[9px] text-destructive">
          {error}
        </p>
      ) : null}
    </section>
  );
};
