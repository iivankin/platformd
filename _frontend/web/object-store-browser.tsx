import {
  ChevronRight,
  Download,
  File,
  Folder,
  HardDrive,
  Trash2,
  Upload,
} from "lucide-react";
import { useRef, useState } from "react";
import type { FormEvent } from "react";

import { objectDownloadURL } from "@/api";
import type { ObjectMetadata, ObjectPage, ObjectPreview } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const formatBytes = (bytes: number) => {
  if (bytes < 1024) {
    return `${bytes.toString()} B`;
  }
  const units = ["KiB", "MiB", "GiB", "TiB"] as const;
  let value = bytes / 1024;
  let unit: (typeof units)[number];
  [unit] = units;
  for (const candidate of units.slice(1)) {
    if (value < 1024) {
      break;
    }
    value /= 1024;
    unit = candidate;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${unit}`;
};

const imageSource = (preview: ObjectPreview) => {
  if (!preview.base64 || !preview.metadata.contentType?.startsWith("image/")) {
    return null;
  }
  const standard = preview.base64.replaceAll("-", "+").replaceAll("_", "/");
  const padded = standard + "=".repeat((4 - (standard.length % 4)) % 4);
  return `data:${preview.metadata.contentType};base64,${padded}`;
};

interface UploadBarProperties {
  busy: boolean;
  onUpload: (key: string, file: File) => Promise<boolean>;
  prefix: string;
}

export const ObjectStoreUploadBar = ({
  busy,
  onUpload,
  prefix,
}: UploadBarProperties) => {
  const fileInput = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [key, setKey] = useState("");

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!file || !key || busy) {
      return;
    }
    if (await onUpload(key, file)) {
      setFile(null);
      setKey("");
      if (fileInput.current) {
        fileInput.current.value = "";
      }
    }
  };

  return (
    <form
      className="grid shrink-0 gap-2 border-b border-border px-4 py-3 sm:grid-cols-[minmax(10rem,1fr)_minmax(12rem,2fr)_auto]"
      onSubmit={(event) => void submit(event)}
    >
      <input
        className="hidden"
        onChange={(event) => {
          const next = event.target.files?.[0] ?? null;
          setFile(next);
          if (next && key === "") {
            setKey(`${prefix}${next.name}`);
          }
        }}
        ref={fileInput}
        type="file"
      />
      <Button
        onClick={() => fileInput.current?.click()}
        type="button"
        variant="outline"
      >
        <File />
        <span className="max-w-40 truncate">{file?.name ?? "Choose file"}</span>
      </Button>
      <Input
        aria-label="Object key for upload"
        onChange={(event) => setKey(event.target.value)}
        placeholder="folder/object.ext"
        value={key}
      />
      <Button disabled={!file || !key || busy} type="submit">
        <Upload />
        {busy ? "Working…" : "Upload"}
      </Button>
    </form>
  );
};

interface ObjectTableProperties {
  canGoBack: boolean;
  currentPrefix: string;
  onNext: () => void;
  onOpenFolder: (prefix: string) => void;
  onPrevious: () => void;
  onSelect: (object: ObjectMetadata) => void;
  page: ObjectPage | null;
  selectedKey?: string;
}

export const ObjectStoreTable = ({
  canGoBack,
  currentPrefix,
  onNext,
  onOpenFolder,
  onPrevious,
  onSelect,
  page,
  selectedKey,
}: ObjectTableProperties) => (
  <div className="min-h-0 overflow-auto border-r border-border">
    <table className="w-full border-collapse text-left text-[10px]">
      <thead className="sticky top-0 z-10 bg-background">
        <tr className="border-b border-border text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
          <th className="px-4 py-2 font-medium">Name</th>
          <th className="w-24 px-3 py-2 font-medium">Size</th>
          <th className="w-40 px-3 py-2 font-medium">Modified</th>
        </tr>
      </thead>
      <tbody>
        {page?.prefixes.map((prefix) => (
          <tr
            className="border-b border-border text-foreground hover:bg-muted/40"
            key={prefix}
          >
            <td className="p-0">
              <button
                className="flex w-full items-center gap-2 px-4 py-2.5 text-left font-mono"
                onClick={() => onOpenFolder(prefix)}
                type="button"
              >
                <Folder className="size-3.5 shrink-0 text-muted-foreground" />
                <span className="truncate">
                  {prefix.slice(currentPrefix.length).replace(/\/$/u, "")}
                </span>
              </button>
            </td>
            <td className="px-3 py-2.5 text-muted-foreground">—</td>
            <td className="px-3 py-2.5 text-muted-foreground">—</td>
          </tr>
        ))}
        {page?.objects.map((object) => (
          <tr
            className={`cursor-pointer border-b border-border hover:bg-muted/40 ${selectedKey === object.objectKey ? "bg-muted/60" : ""}`}
            key={object.objectKey}
            onClick={() => onSelect(object)}
          >
            <td className="max-w-0 px-4 py-2.5 font-mono break-all">
              <span className="inline-flex items-center gap-2">
                <File className="size-3.5 shrink-0 text-muted-foreground" />
                {object.objectKey.slice(currentPrefix.length) ||
                  object.objectKey}
              </span>
            </td>
            <td className="px-3 py-2.5 text-muted-foreground">
              {formatBytes(object.size)}
            </td>
            <td className="px-3 py-2.5 text-muted-foreground">
              {new Date(object.updatedAt).toLocaleString()}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
    {page && page.objects.length === 0 && page.prefixes.length === 0 ? (
      <div className="grid min-h-52 place-items-center px-6 text-center text-[10px] text-muted-foreground">
        This folder is empty.
      </div>
    ) : null}
    <div className="flex items-center justify-end gap-2 border-t border-border px-4 py-3">
      <Button
        disabled={!canGoBack}
        onClick={onPrevious}
        size="sm"
        variant="ghost"
      >
        Previous
      </Button>
      <Button
        disabled={!page?.nextContinuationToken}
        onClick={onNext}
        size="sm"
        variant="ghost"
      >
        Next
      </Button>
    </div>
  </div>
);

interface PathBarProperties {
  onNavigate: (prefix: string) => void;
  prefix: string;
}

export const ObjectStorePathBar = ({
  onNavigate,
  prefix,
}: PathBarProperties) => {
  const segments = prefix.split("/").filter(Boolean);
  const crumbs = segments.map((label, index) => ({
    label,
    prefix: `${segments.slice(0, index + 1).join("/")}/`,
  }));

  return (
    <nav
      aria-label="Object folder path"
      className="flex min-h-10 shrink-0 items-center gap-1 overflow-x-auto border-b border-border px-4 text-[10px]"
    >
      <button
        className={`shrink-0 font-mono hover:text-foreground ${prefix === "" ? "text-foreground" : "text-muted-foreground"}`}
        disabled={prefix === ""}
        onClick={() => onNavigate("")}
        type="button"
      >
        Bucket
      </button>
      {crumbs.map((crumb, index) => {
        const current = index === crumbs.length - 1;
        return (
          <span className="flex shrink-0 items-center gap-1" key={crumb.prefix}>
            <ChevronRight className="size-3 text-muted-foreground" />
            <button
              className={`font-mono hover:text-foreground ${current ? "text-foreground" : "text-muted-foreground"}`}
              disabled={current}
              onClick={() => onNavigate(crumb.prefix)}
              type="button"
            >
              {crumb.label}
            </button>
          </span>
        );
      })}
    </nav>
  );
};

interface PreviewPaneProperties {
  busy: boolean;
  onDelete: () => Promise<void>;
  preview: ObjectPreview | null;
  projectID: string;
  selected: ObjectMetadata | null;
  storeID: string;
}

export const ObjectStorePreviewPane = ({
  busy,
  onDelete,
  preview,
  projectID,
  selected,
  storeID,
}: PreviewPaneProperties) => {
  const [deleteArmed, setDeleteArmed] = useState(false);
  if (!selected) {
    return (
      <div className="grid min-h-72 place-items-center px-8 text-center">
        <div>
          <HardDrive className="mx-auto size-6 text-muted-foreground" />
          <p className="mt-4 text-xs font-medium">Select an object</p>
          <p className="mt-2 text-[10px] leading-5 text-muted-foreground">
            Inspect safe previews, download payloads, or delete an exact key.
          </p>
        </div>
      </div>
    );
  }

  const previewImage = preview ? imageSource(preview) : null;
  return (
    <>
      <div className="flex items-center gap-2 border-b border-border px-4 py-3">
        <p className="min-w-0 flex-1 truncate font-mono text-[10px]">
          {selected.objectKey}
        </p>
        <Button
          onClick={() =>
            window.location.assign(
              objectDownloadURL(projectID, storeID, selected.objectKey)
            )
          }
          size="sm"
          variant="outline"
        >
          <Download />
          Download
        </Button>
        {deleteArmed ? (
          <Button
            disabled={busy}
            onClick={() => void onDelete()}
            size="sm"
            variant="destructive"
          >
            Delete now
          </Button>
        ) : (
          <Button
            aria-label="Prepare to delete selected object"
            disabled={busy}
            onClick={() => setDeleteArmed(true)}
            size="icon"
            variant="destructive"
          >
            <Trash2 />
          </Button>
        )}
      </div>
      <dl className="grid grid-cols-2 border-b border-border text-[9px]">
        <div className="border-r border-border px-4 py-3">
          <dt className="text-muted-foreground">Content type</dt>
          <dd className="mt-1 break-all">
            {selected.contentType || "application/octet-stream"}
          </dd>
        </div>
        <div className="px-4 py-3">
          <dt className="text-muted-foreground">ETag</dt>
          <dd className="mt-1 break-all">{selected.etag}</dd>
        </div>
      </dl>
      {preview?.allowed && previewImage ? (
        <div className="grid min-h-72 place-items-center bg-muted/20 p-5">
          <img
            alt={selected.objectKey}
            className="max-h-[28rem] max-w-full object-contain"
            src={previewImage}
          />
        </div>
      ) : null}
      {preview?.allowed && preview.text !== undefined ? (
        <pre className="overflow-auto p-4 font-mono text-[10px] leading-5 break-words whitespace-pre-wrap">
          {preview.text}
        </pre>
      ) : null}
      {preview && !preview.allowed ? (
        <p className="px-4 py-8 text-[10px] leading-5 text-muted-foreground">
          Inline preview is unavailable for large, binary, or secret-looking
          objects. Download remains available.
        </p>
      ) : null}
    </>
  );
};
