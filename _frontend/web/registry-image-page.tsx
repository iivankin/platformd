import { ArrowLeft, FileJson } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import {
  deleteRegistryImage,
  deleteRegistryTag,
  fetchRegistryImage,
} from "@/api";
import type { RegistryImage } from "@/api";
import { RegistryImageDetailPanel } from "@/registry-image-detail-panel";
import { useRegistryRepository } from "@/use-registry-repository";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export const RegistryImagePage = () => {
  const navigate = useNavigate();
  const { imageDigest = "", repositoryID = "" } = useParams();
  const {
    hostname,
    refresh: refreshRepository,
    repository,
  } = useRegistryRepository(repositoryID);
  const [image, setImage] = useState<RegistryImage>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const loadImage = useCallback(async () => {
    try {
      setImage(await fetchRegistryImage(repositoryID, imageDigest));
      setError(undefined);
    } catch (loadError) {
      setError(errorText(loadError, "Unable to load image details"));
    }
  }, [imageDigest, repositoryID]);

  useEffect(() => {
    const load = async () => {
      await loadImage();
    };
    void load();
  }, [loadImage]);

  const copyPull = async () => {
    if (!(image && repository && hostname)) {
      return;
    }
    const reference = image.tags[0] ?? image.digest;
    await navigator.clipboard.writeText(
      `${hostname}/${repository.name}:${reference}`.replace(
        `:${image.digest}`,
        `@${image.digest}`
      )
    );
  };

  const removeTag = async (tag: string) => {
    if (busy) {
      return;
    }
    setBusy(true);
    try {
      await deleteRegistryTag(repositoryID, tag);
      await loadImage();
      refreshRepository();
    } catch (deleteError) {
      setError(errorText(deleteError, "Unable to delete tag"));
    } finally {
      setBusy(false);
    }
  };

  const removeImage = async () => {
    if (!(image && repository) || busy) {
      return;
    }
    setBusy(true);
    try {
      await deleteRegistryImage(repositoryID, image.digest);
      void navigate(`/registry/repositories/${repositoryID}/images`);
    } catch (deleteError) {
      setError(errorText(deleteError, "Unable to delete image"));
      setBusy(false);
    }
  };

  const imagesPath = `/registry/repositories/${repositoryID}/images`;

  return (
    <div className="min-h-full animate-in duration-200 fade-in slide-in-from-bottom-1">
      <section className="flex min-h-20 items-center gap-4 border-b border-border px-5 py-4">
        <Link
          aria-label="Back to repository images"
          className="grid size-8 shrink-0 place-items-center border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          to={imagesPath}
        >
          <ArrowLeft className="size-3.5" />
        </Link>
        <span className="grid size-9 shrink-0 place-items-center bg-muted/50">
          <FileJson className="size-4 text-muted-foreground" />
        </span>
        <div className="min-w-0">
          <p className="text-[8px] tracking-[0.14em] text-muted-foreground uppercase">
            {repository?.name ?? "Repository image"}
          </p>
          <h2 className="mt-1 truncate text-sm font-medium">
            {image?.tags[0] ?? "Untagged image"}
          </h2>
          <p className="mt-1 truncate font-mono text-[9px] text-muted-foreground">
            {imageDigest}
          </p>
        </div>
      </section>

      <RegistryImageDetailPanel
        busy={busy}
        canCopy={Boolean(hostname && repository)}
        onCopy={() => void copyPull()}
        onDelete={() => void removeImage()}
        onDeleteTag={(tag) => void removeTag(tag)}
        selected={image}
      />

      {error ? (
        <p className="border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
};
