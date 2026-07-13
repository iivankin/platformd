import {
  ChevronLeft,
  ChevronRight,
  Copy,
  FileJson,
  PackageX,
  RefreshCw,
  Tag,
  Trash2,
} from "lucide-react";
import { useEffect, useState } from "react";

import {
  deleteRegistryImage,
  deleteRegistryRepository,
  deleteRegistryTag,
  fetchRegistryImage,
  fetchRegistryImages,
  setRegistryRepositoryPublicPull,
} from "@/api";
import type { RegistryImage, RegistryRepository } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { RegistryCleanup } from "@/registry-cleanup";
import { RegistryCredentials } from "@/registry-credentials";
import { formatRegistryBytes } from "@/registry-repository-list";
import { ResourceBackupPanel } from "@/resource-backup-panel";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const shortDigest = (digest: string) =>
  `${digest.slice(0, 19)}…${digest.slice(-8)}`;

export const RegistryRepositoryDetail = ({
  hostname,
  onDeleted,
  onChanged,
  repository,
}: {
  hostname: string;
  onDeleted: (repositoryID: string) => void;
  onChanged: () => void;
  repository: RegistryRepository;
}) => {
  const [images, setImages] = useState<RegistryImage[]>([]);
  const [selected, setSelected] = useState<RegistryImage>();
  const [cursor, setCursor] = useState("");
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [busy, setBusy] = useState("");
  const [error, setError] = useState<string>();
  const [deleteName, setDeleteName] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const page = await fetchRegistryImages(
          repository.id,
          { after: cursor },
          controller.signal
        );
        setImages(page.images);
        setNextCursor(page.nextCursor);
        setSelected(undefined);
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load Registry images"));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [cursor, refreshVersion, repository.id]);

  const inspect = async (image: RegistryImage) => {
    setSelected(image);
    setError(undefined);
    try {
      setSelected(await fetchRegistryImage(repository.id, image.digest));
    } catch (loadError) {
      setError(errorText(loadError, "Unable to load manifest details"));
    }
  };

  const removeTag = async (tag: string) => {
    if (busy) {
      return;
    }
    setBusy(`tag:${tag}`);
    try {
      await deleteRegistryTag(repository.id, tag);
      setRefreshVersion((value) => value + 1);
      onChanged();
    } catch (deleteError) {
      setError(errorText(deleteError, "Unable to delete tag"));
    } finally {
      setBusy("");
    }
  };

  const removeImage = async () => {
    if (!selected || busy) {
      return;
    }
    setBusy("image");
    try {
      await deleteRegistryImage(repository.id, selected.digest);
      setRefreshVersion((value) => value + 1);
      onChanged();
    } catch (deleteError) {
      setError(errorText(deleteError, "Unable to delete image"));
    } finally {
      setBusy("");
    }
  };

  const removeRepository = async () => {
    if (deleteName !== repository.name || busy) {
      return;
    }
    setBusy("repository");
    try {
      await deleteRegistryRepository(repository.id, deleteName);
      onDeleted(repository.id);
    } catch (deleteError) {
      setError(errorText(deleteError, "Unable to delete repository"));
    } finally {
      setBusy("");
    }
  };

  const togglePublicPull = async () => {
    if (busy) {
      return;
    }
    setBusy("public-pull");
    try {
      await setRegistryRepositoryPublicPull(
        repository.id,
        !repository.publicPull
      );
      onChanged();
    } catch (updateError) {
      setError(errorText(updateError, "Unable to update repository access"));
    } finally {
      setBusy("");
    }
  };

  const copyPull = async (image: RegistryImage) => {
    const reference = image.tags[0] ?? image.digest;
    await navigator.clipboard.writeText(
      `${hostname}/${repository.name}:${reference}`.replace(
        `:${image.digest}`,
        `@${image.digest}`
      )
    );
  };

  return (
    <div className="min-h-0 overflow-auto">
      <div className="flex h-12 items-center border-b border-border px-4">
        <div className="min-w-0">
          <h2 className="truncate font-mono text-xs font-medium">
            {repository.name}
          </h2>
          <p className="mt-0.5 text-[9px] text-muted-foreground">
            {hostname
              ? `${hostname}/${repository.name}`
              : "Configure a hostname before push or pull"}
          </p>
        </div>
        <Button
          aria-label="Refresh images"
          className="ml-auto"
          onClick={() => setRefreshVersion((value) => value + 1)}
          size="icon"
          variant="ghost"
        >
          <RefreshCw />
        </Button>
      </div>

      <div className="grid grid-cols-4 border-b border-border text-[9px]">
        {[
          ["Access", repository.publicPull ? "Public pull" : "Private"],
          ["Manifests", repository.manifestCount.toString()],
          ["Blobs", repository.blobCount.toString()],
          [
            "Payload",
            `${formatRegistryBytes(repository.referencedBlobBytes)} / ${formatRegistryBytes(repository.totalBlobBytes)}`,
          ],
        ].map(([label, value]) => (
          <div
            className="border-r border-border px-4 py-3 last:border-r-0"
            key={label}
          >
            <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              {label}
            </p>
            <p className="mt-1">{value}</p>
          </div>
        ))}
      </div>

      <ResourceBackupPanel resourceID={repository.id} resourceKind="registry" />

      <div className="flex items-center gap-3 border-b border-border px-4 py-3">
        <div>
          <p className="text-[10px] font-medium">Anonymous pulls</p>
          <p className="mt-0.5 text-[9px] text-muted-foreground">
            {repository.publicPull
              ? "Anyone can pull this repository. Push still requires a robot credential."
              : "Every pull and push requires a repository robot credential."}
          </p>
        </div>
        <Button
          className="ml-auto"
          disabled={Boolean(busy)}
          onClick={() => void togglePublicPull()}
          variant="outline"
        >
          {repository.publicPull ? "Make private" : "Allow public pull"}
        </Button>
      </div>

      <RegistryCredentials hostname={hostname} repositoryID={repository.id} />
      <RegistryCleanup onChanged={onChanged} repositoryID={repository.id} />

      <div className="grid min-h-80 lg:grid-cols-[minmax(0,3fr)_minmax(19rem,2fr)]">
        <div className="border-r border-border">
          <div className="grid grid-cols-[minmax(0,1fr)_140px_110px] border-b border-border bg-muted/30 px-4 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            <span>Image / digest</span>
            <span>Media</span>
            <span>Pushed</span>
          </div>
          {images.map((image) => (
            <button
              className={`grid w-full grid-cols-[minmax(0,1fr)_140px_110px] gap-3 border-b border-border px-4 py-3 text-left hover:bg-muted/40 ${selected?.digest === image.digest ? "bg-muted/60" : ""}`}
              key={image.digest}
              onClick={() => void inspect(image)}
              type="button"
            >
              <span className="min-w-0">
                <span className="flex flex-wrap gap-1">
                  {image.tags.length ? (
                    image.tags.map((tag) => (
                      <span
                        className="border border-border bg-background px-1.5 py-0.5 font-mono text-[8px]"
                        key={tag}
                      >
                        {tag}
                      </span>
                    ))
                  ) : (
                    <span className="text-[9px] text-muted-foreground">
                      untagged
                    </span>
                  )}
                </span>
                <span
                  className="mt-2 block truncate font-mono text-[9px] text-muted-foreground"
                  title={image.digest}
                >
                  {shortDigest(image.digest)}
                </span>
              </span>
              <span
                className="truncate pt-0.5 text-[9px] text-muted-foreground"
                title={image.mediaType}
              >
                {image.mediaType.includes("index") ||
                image.mediaType.includes("list")
                  ? "index"
                  : "image"}
              </span>
              <span className="pt-0.5 text-[9px] text-muted-foreground">
                {new Date(image.pushedAt).toLocaleDateString()}
              </span>
            </button>
          ))}
          {images.length === 0 ? (
            <div className="grid min-h-52 place-items-center px-6 text-center text-[10px] text-muted-foreground">
              No manifests have been pushed.
            </div>
          ) : null}
          <div className="flex justify-end gap-1 border-b border-border px-4 py-2">
            <Button
              disabled={cursorHistory.length === 0}
              onClick={() => {
                setCursor(cursorHistory.at(-1) ?? "");
                setCursorHistory((value) => value.slice(0, -1));
              }}
              size="icon"
              variant="ghost"
            >
              <ChevronLeft />
            </Button>
            <Button
              disabled={!nextCursor}
              onClick={() => {
                setCursorHistory((value) => [...value, cursor]);
                setCursor(nextCursor);
              }}
              size="icon"
              variant="ghost"
            >
              <ChevronRight />
            </Button>
          </div>
        </div>

        <div className="min-w-0">
          {selected ? (
            <>
              <div className="flex items-center gap-2 border-b border-border px-4 py-3">
                <FileJson className="size-3.5 text-muted-foreground" />
                <span
                  className="min-w-0 flex-1 truncate font-mono text-[9px]"
                  title={selected.digest}
                >
                  {shortDigest(selected.digest)}
                </span>
                {hostname ? (
                  <Button
                    aria-label="Copy pull reference"
                    onClick={() => void copyPull(selected)}
                    size="icon"
                    variant="ghost"
                  >
                    <Copy />
                  </Button>
                ) : null}
                <Button
                  aria-label={`Delete manifest ${selected.digest}`}
                  disabled={Boolean(busy)}
                  onClick={() => void removeImage()}
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
                        disabled={Boolean(busy)}
                        onClick={() => void removeTag(tag)}
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
                  <dt className="text-muted-foreground">Manifest</dt>
                  <dd className="mt-1">
                    {formatRegistryBytes(selected.manifestSize)}
                  </dd>
                </div>
                <div className="px-4 py-3">
                  <dt className="text-muted-foreground">Referenced blobs</dt>
                  <dd className="mt-1">
                    {formatRegistryBytes(selected.referencedBlobBytes)}
                  </dd>
                </div>
              </dl>
              <pre className="max-h-96 overflow-auto px-4 py-3 font-mono text-[9px] leading-4 break-all whitespace-pre-wrap text-muted-foreground">
                {selected.manifest
                  ? JSON.stringify(selected.manifest, null, 2)
                  : "Loading manifest…"}
              </pre>
            </>
          ) : (
            <div className="grid min-h-72 place-items-center px-8 text-center">
              <div>
                <PackageX className="mx-auto size-6 text-muted-foreground" />
                <p className="mt-4 text-xs font-medium">Select an image</p>
                <p className="mt-2 text-[10px] leading-5 text-muted-foreground">
                  Inspect manifest JSON, platforms, blob digests, and tags.
                </p>
              </div>
            </div>
          )}
        </div>
      </div>

      <div className="border-y border-destructive/25 bg-destructive/5 px-4 py-4">
        <p className="text-[10px] font-medium text-destructive">
          Delete repository
        </p>
        <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
          Removes manifests, credentials, uploads, and every repository-local
          blob. Type the exact name to continue.
        </p>
        <div className="mt-3 flex max-w-xl gap-2">
          <Input
            onChange={(event) => setDeleteName(event.target.value)}
            placeholder={repository.name}
            value={deleteName}
          />
          <Button
            disabled={deleteName !== repository.name || Boolean(busy)}
            onClick={() => void removeRepository()}
            variant="destructive"
          >
            Delete repository
          </Button>
        </div>
      </div>
      {error ? (
        <p
          aria-live="polite"
          className="border-b border-destructive/30 px-4 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </div>
  );
};
