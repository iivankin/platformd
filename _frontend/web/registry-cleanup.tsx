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
          <p className="text-[10px] font-medium">Unused image data</p>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Finds data no longer used by an image. Recently unreferenced data is
            kept for 24 hours before it can be deleted.
          </p>
        </div>
        <Button
          disabled={busy}
          onClick={() => void run(true)}
          size="sm"
          variant="outline"
        >
          <RefreshCw />
          Scan
        </Button>
        {preview && !preview.deleted && preview.blobCount > 0 ? (
          <Button
            disabled={busy}
            onClick={() => void run(false)}
            size="sm"
            variant="destructive"
          >
            Delete unused data
          </Button>
        ) : null}
      </div>
      {preview ? (
        <div className="border-t border-border px-4 py-2 text-[9px] text-muted-foreground">
          {preview.blobCount === 0
            ? "No unused image data found."
            : `${preview.deleted ? "Deleted" : "Found"} ${preview.blobCount.toLocaleString()} unused objects · ${formatRegistryBytes(preview.bytes)}`}
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
