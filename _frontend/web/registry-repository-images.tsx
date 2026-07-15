import { ChevronLeft, ChevronRight, RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";

import { fetchRegistryImages } from "@/api";
import type { RegistryImage, RegistryRepository } from "@/api";
import { Button } from "@/components/ui/button";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export const RegistryRepositoryImages = ({
  repository,
}: {
  repository: RegistryRepository;
}) => {
  const navigate = useNavigate();
  const [images, setImages] = useState<RegistryImage[]>([]);
  const [cursor, setCursor] = useState("");
  const [cursorHistory, setCursorHistory] = useState<string[]>([]);
  const [nextCursor, setNextCursor] = useState("");
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [error, setError] = useState<string>();

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
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load images"));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [cursor, refreshVersion, repository.id]);

  return (
    <div>
      <section className="flex min-h-14 items-center justify-between border-b border-border px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">Images</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Select an image to open its manifest workspace.
          </p>
        </div>
        <Button
          aria-label="Refresh images"
          onClick={() => setRefreshVersion((value) => value + 1)}
          size="icon"
          variant="ghost"
        >
          <RefreshCw />
        </Button>
      </section>

      <div className="grid grid-cols-[minmax(0,1fr)_8rem_8rem] border-b border-border bg-muted/25 px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
        <span>Version</span>
        <span>Type</span>
        <span>Pushed</span>
      </div>
      {images.map((image) => (
        <button
          className="grid w-full grid-cols-[minmax(0,1fr)_8rem_8rem] items-center border-b border-border px-5 py-4 text-left hover:bg-muted/40"
          key={image.digest}
          onClick={() =>
            void navigate(
              `/registry/repositories/${repository.id}/images/${encodeURIComponent(image.digest)}`
            )
          }
          type="button"
        >
          <span className="flex min-w-0 flex-wrap gap-1">
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
              <span className="text-[9px] text-muted-foreground">Untagged</span>
            )}
          </span>
          <span className="text-[9px] text-muted-foreground">
            {image.mediaType.includes("index") ||
            image.mediaType.includes("list")
              ? "Multi-platform"
              : "Image"}
          </span>
          <span className="text-[9px] text-muted-foreground">
            {new Date(image.pushedAt).toLocaleDateString()}
          </span>
        </button>
      ))}
      {images.length === 0 ? (
        <div className="grid min-h-52 place-items-center border-b border-border px-6 text-center text-[10px] text-muted-foreground">
          No images have been pushed.
        </div>
      ) : null}
      <div className="flex justify-end gap-1 border-b border-border px-5 py-2">
        <Button
          aria-label="Previous image page"
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
          aria-label="Next image page"
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
      {error ? (
        <p className="border-b border-destructive/30 px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
};
