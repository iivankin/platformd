import { Copy, FileJson, PackageX, Tag, Trash2 } from "lucide-react";

import type { RegistryImage } from "@/api";
import { Button } from "@/components/ui/button";
import { formatRegistryBytes } from "@/registry-repository-list";

export const RegistryImageDetailPanel = ({
  busy,
  canCopy,
  onCopy,
  onDelete,
  onDeleteTag,
  selected,
}: {
  busy: boolean;
  canCopy: boolean;
  onCopy: () => void;
  onDelete: () => void;
  onDeleteTag: (tag: string) => void;
  selected?: RegistryImage;
}) => (
  <div className="min-w-0">
    {selected ? (
      <>
        <div className="flex items-center gap-2 border-b border-border px-4 py-3">
          <FileJson className="size-3.5 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate text-[10px] font-medium">
            {selected.tags[0] ?? "Untagged image"}
          </span>
          {canCopy ? (
            <Button
              aria-label="Copy pull reference"
              onClick={onCopy}
              size="icon"
              variant="ghost"
            >
              <Copy />
            </Button>
          ) : null}
          <Button
            aria-label="Delete image"
            disabled={busy}
            onClick={onDelete}
            size="icon"
            variant="destructive"
          >
            <Trash2 />
          </Button>
        </div>
        <div className="border-b border-border px-4 py-3 text-[9px]">
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Tags
          </p>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {selected.tags.map((tag) => (
              <span
                className="inline-flex items-center border border-border"
                key={tag}
              >
                <span className="px-2 py-1 font-mono">{tag}</span>
                <button
                  aria-label={`Delete tag ${tag}`}
                  className="border-l border-border px-1.5 py-1 text-muted-foreground hover:text-destructive"
                  disabled={busy}
                  onClick={() => onDeleteTag(tag)}
                  type="button"
                >
                  <Tag className="size-3" />
                </button>
              </span>
            ))}
          </div>
        </div>
        <dl className="grid grid-cols-2 border-b border-border text-[9px]">
          <div className="border-r border-border px-4 py-3">
            <dt className="text-muted-foreground">Manifest size</dt>
            <dd className="mt-1">
              {formatRegistryBytes(selected.manifestSize)}
            </dd>
          </div>
          <div className="px-4 py-3">
            <dt className="text-muted-foreground">Image data</dt>
            <dd className="mt-1">
              {formatRegistryBytes(selected.referencedBlobBytes)}
            </dd>
          </div>
        </dl>
        <details>
          <summary className="cursor-pointer border-b border-border px-4 py-3 text-[9px] text-muted-foreground hover:text-foreground">
            Advanced manifest details
          </summary>
          <div className="border-b border-border px-4 py-3 font-mono text-[9px] break-all text-muted-foreground">
            {selected.digest}
          </div>
          <pre className="max-h-96 overflow-auto px-4 py-3 font-mono text-[9px] leading-4 break-all whitespace-pre-wrap text-muted-foreground">
            {selected.manifest
              ? JSON.stringify(selected.manifest, null, 2)
              : "Loading manifest…"}
          </pre>
        </details>
      </>
    ) : (
      <div className="grid min-h-72 place-items-center px-8 text-center">
        <div>
          <PackageX className="mx-auto size-6 text-muted-foreground" />
          <p className="mt-4 text-xs font-medium">Select an image</p>
          <p className="mt-2 text-[10px] leading-5 text-muted-foreground">
            Image details appear here.
          </p>
        </div>
      </div>
    )}
  </div>
);
