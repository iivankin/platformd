import { Eraser, LoaderCircle } from "lucide-react";
import { useState } from "react";

import { forceImageGarbageCollection } from "@/api";
import type { ImageGarbageCollectionResult } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";

const formatBytes = (value: number) => {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let current = value;
  let unit = 0;
  while (current >= 1024 && unit < units.length - 1) {
    current /= 1024;
    unit += 1;
  }
  return `${current.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

const resultText = (result: ImageGarbageCollectionResult) => {
  const removed =
    result.buildCacheImagesRemoved +
    result.finalImagesRemoved +
    result.orphanLayersRemoved;
  if (removed === 0) {
    return result.skipped === 0
      ? "No unused container image data found."
      : `No data removed · ${result.skipped.toLocaleString()} items skipped`;
  }
  const parts = [
    `${result.buildCacheImagesRemoved.toLocaleString()} build cache images`,
    `${result.finalImagesRemoved.toLocaleString()} inactive final images`,
    `${result.orphanLayersRemoved.toLocaleString()} orphan layers`,
    `${formatBytes(result.removedBytes)} reclaimed`,
  ];
  if (result.skipped > 0) {
    parts.push(`${result.skipped.toLocaleString()} skipped`);
  }
  return parts.join(" · ");
};

export const ImageGarbageCollectionOperation = () => {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [result, setResult] = useState<ImageGarbageCollectionResult>();

  const run = async () => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      setResult(await forceImageGarbageCollection());
    } catch (cleanupError) {
      setError(
        cleanupError instanceof Error
          ? cleanupError.message
          : "Container image garbage collection failed"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <SectionCard>
      <div className="flex flex-col gap-5 px-5 py-6 md:flex-row md:items-center">
        <div className="grid size-10 shrink-0 place-items-center bg-muted">
          <Eraser className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
            Container storage
          </p>
          <p className="mt-1 text-sm font-medium">Image garbage collection</p>
          <p className="mt-2 max-w-2xl text-[10px] leading-4 text-muted-foreground">
            Remove disposable build cache and orphan layers now. Inactive final
            images keep their 14-day retention.
          </p>
        </div>
        <Button
          className="shrink-0"
          disabled={busy}
          onClick={() => void run()}
          type="button"
          variant="destructive"
        >
          {busy ? <LoaderCircle className="animate-spin" /> : <Eraser />}
          {busy ? "Running GC…" : "Force GC"}
        </Button>
      </div>
      {result ? (
        <p className="border-t border-border px-5 py-3 text-[9px] text-muted-foreground">
          {resultText(result)}
        </p>
      ) : null}
      {error ? (
        <p className="border-t border-destructive/30 px-5 py-3 text-[9px] text-destructive">
          {error}
        </p>
      ) : null}
    </SectionCard>
  );
};
