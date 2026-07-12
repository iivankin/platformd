import { useState } from "react";

import type { RedisMutationInput, RedisPreview } from "@/api";
import { RedisKeyControls } from "@/redis-key-controls";
import { RedisKeyEntryEditor } from "@/redis-key-entry-editor";
import { RedisKeyItems } from "@/redis-key-items";

interface RedisKeyEditorProperties {
  busy: boolean;
  keyBase64: string;
  onMutate: (input: RedisMutationInput) => Promise<void>;
  preview: RedisPreview;
}

export const RedisKeyEditor = ({
  busy,
  keyBase64,
  onMutate,
  preview,
}: RedisKeyEditorProperties) => {
  const [error, setError] = useState<string | null>(null);
  const apply = async (input: RedisMutationInput) => {
    setError(null);
    try {
      await onMutate(input);
    } catch (mutationError) {
      setError(
        mutationError instanceof Error
          ? mutationError.message
          : "Redis mutation failed"
      );
    }
  };

  return (
    <section className="border-b border-border">
      <div className="flex items-center justify-between gap-3 border-b border-border px-4 py-3">
        <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
          {preview.type} · {preview.length.toLocaleString()} entries
        </h3>
        {preview.truncated ? (
          <span className="text-[9px] text-amber-600">Preview bounded</span>
        ) : null}
      </div>
      <RedisKeyEntryEditor
        apply={apply}
        busy={busy}
        keyBase64={keyBase64}
        preview={preview}
      />
      <RedisKeyItems
        apply={apply}
        busy={busy}
        keyBase64={keyBase64}
        preview={preview}
      />
      <RedisKeyControls apply={apply} busy={busy} keyBase64={keyBase64} />
      {error ? (
        <p
          aria-live="polite"
          className="border-t border-border px-4 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </section>
  );
};
