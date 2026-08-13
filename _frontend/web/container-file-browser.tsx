import { Download, FolderTree, RefreshCw, Upload } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { FormEvent } from "react";

import {
  containerFileContentURL,
  fetchContainerFiles,
  uploadContainerFile,
} from "@/api";
import type { ContainerFileEntry, ContainerResourceKind } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { ContainerFileTree } from "@/container-file-tree";
import { cn } from "@/lib/utils";

interface ContainerFileBrowserProperties {
  className?: string;
  projectID: string;
  resourceID: string;
  resourceKind: ContainerResourceKind;
}

const formatBytes = (value: number) => {
  if (value < 1024) {
    return `${value} B`;
  }
  if (value < 1024 * 1024) {
    return `${(value / 1024).toFixed(1)} KiB`;
  }
  return `${(value / (1024 * 1024)).toFixed(1)} MiB`;
};

const parentPath = (value: string) => {
  const slash = value.lastIndexOf("/");
  return slash <= 0 ? "/" : value.slice(0, slash);
};

const joinPath = (directory: string, name: string) =>
  directory === "/" ? `/${name}` : `${directory}/${name}`;

const uploadDirectory = (
  selected: ContainerFileEntry | undefined,
  root: string
) => {
  if (!selected) {
    return root;
  }
  return selected.directory ? selected.path : parentPath(selected.path);
};

export const ContainerFileBrowser = ({
  className,
  projectID,
  resourceID,
  resourceKind,
}: ContainerFileBrowserProperties) => {
  const [rootInput, setRootInput] = useState("/");
  const [currentRoot, setCurrentRoot] = useState("/");
  const [entriesByDirectory, setEntriesByDirectory] = useState<
    ReadonlyMap<string, readonly ContainerFileEntry[]>
  >(() => new Map());
  const [expandedPaths, setExpandedPaths] = useState<ReadonlySet<string>>(
    () => new Set()
  );
  const [loadingPaths, setLoadingPaths] = useState<ReadonlySet<string>>(
    () => new Set()
  );
  const [selectedPath, setSelectedPath] = useState<string>();
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<string>();
  const uploadRef = useRef<HTMLInputElement>(null);
  const requestsRef = useRef(new Map<string, AbortController>());
  const generationRef = useRef(0);

  const loadDirectory = useCallback(
    async (path: string, replaceRoot = false) => {
      if (!path.startsWith("/") || path.includes("\0")) {
        setError("Path must be absolute");
        return;
      }

      let generation = generationRef.current;
      if (replaceRoot) {
        generation += 1;
        generationRef.current = generation;
        for (const controller of requestsRef.current.values()) {
          controller.abort();
        }
        requestsRef.current.clear();
      } else if (requestsRef.current.has(path)) {
        return;
      }

      const controller = new AbortController();
      requestsRef.current.set(path, controller);
      setLoadingPaths((current) =>
        replaceRoot ? new Set([path]) : new Set(current).add(path)
      );
      try {
        const tree = await fetchContainerFiles(
          projectID,
          resourceKind,
          resourceID,
          path,
          controller.signal
        );
        if (generation !== generationRef.current) {
          return;
        }
        setEntriesByDirectory((current) => {
          const next = replaceRoot ? new Map() : new Map(current);
          next.set(tree.root, tree.entries);
          return next;
        });
        if (replaceRoot) {
          setCurrentRoot(tree.root);
          setRootInput(tree.root);
          setExpandedPaths(new Set());
          setSelectedPath(undefined);
        } else {
          setExpandedPaths((current) => new Set(current).add(tree.root));
        }
        setError(undefined);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load container files"
        );
      } finally {
        if (requestsRef.current.get(path) === controller) {
          requestsRef.current.delete(path);
          setLoadingPaths((current) => {
            const next = new Set(current);
            next.delete(path);
            return next;
          });
        }
      }
    },
    [projectID, resourceID, resourceKind]
  );

  useEffect(() => {
    const requests = requestsRef.current;
    const generation = generationRef;
    const frame = requestAnimationFrame(() => void loadDirectory("/", true));
    return () => {
      cancelAnimationFrame(frame);
      generation.current += 1;
      for (const controller of requests.values()) {
        controller.abort();
      }
      requests.clear();
    };
  }, [loadDirectory]);

  const entriesByPath = useMemo(() => {
    const result = new Map<string, ContainerFileEntry>();
    for (const entries of entriesByDirectory.values()) {
      for (const entry of entries) {
        result.set(entry.path, entry);
      }
    }
    return result;
  }, [entriesByDirectory]);
  const selected = selectedPath ? entriesByPath.get(selectedPath) : undefined;
  const busy = loadingPaths.size > 0 || uploading;

  const submitRoot = (event: FormEvent) => {
    event.preventDefault();
    void loadDirectory(rootInput, true);
  };

  const toggleDirectory = (entry: ContainerFileEntry) => {
    if (!entry.directory) {
      return;
    }
    if (expandedPaths.has(entry.path)) {
      setExpandedPaths((current) => {
        const next = new Set(current);
        next.delete(entry.path);
        return next;
      });
      return;
    }
    if (entriesByDirectory.has(entry.path)) {
      setExpandedPaths((current) => new Set(current).add(entry.path));
      return;
    }
    void loadDirectory(entry.path);
  };

  const upload = async (file: File) => {
    if (file.name.includes("/")) {
      setError("File name is invalid");
      return;
    }
    const directory = uploadDirectory(selected, currentRoot);
    setUploading(true);
    try {
      await uploadContainerFile(
        projectID,
        resourceKind,
        resourceID,
        joinPath(directory, file.name),
        file
      );
      await loadDirectory(directory);
      setError(undefined);
    } catch (uploadError) {
      setError(
        uploadError instanceof Error
          ? uploadError.message
          : "Unable to upload file"
      );
    } finally {
      setUploading(false);
    }
  };

  return (
    <SectionCard className={cn("bg-background", className)}>
      <header className="flex min-h-11 flex-wrap items-center gap-2 border-b border-border px-4 py-2">
        <FolderTree className="size-4 text-muted-foreground" />
        <div>
          <h3 className="text-[10px] font-medium">Container files</h3>
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Live filesystem · Lazy tree
          </p>
        </div>
        <form className="ml-4 flex min-w-64 flex-1 gap-2" onSubmit={submitRoot}>
          <Input
            aria-label="Container root path"
            className="h-8 font-mono text-[10px]"
            onChange={(event) => setRootInput(event.target.value)}
            value={rootInput}
          />
          <Button disabled={busy} size="sm" type="submit" variant="outline">
            <RefreshCw />
            Open
          </Button>
        </form>
        <input
          className="hidden"
          onChange={(event) => {
            const file = event.target.files?.[0];
            if (file) {
              void upload(file);
            }
            event.target.value = "";
          }}
          ref={uploadRef}
          type="file"
        />
        <Button
          disabled={busy}
          onClick={() => uploadRef.current?.click()}
          size="sm"
          variant="outline"
        >
          <Upload />
          Upload
        </Button>
      </header>

      {error ? (
        <p className="border-b border-destructive/30 bg-destructive/5 px-4 py-2 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}

      <div className="grid h-[24rem] min-h-0 grid-cols-[minmax(16rem,0.9fr)_minmax(15rem,1.1fr)]">
        <div className="min-h-0 min-w-0 overflow-hidden border-r border-border">
          {entriesByDirectory.has(currentRoot) ? (
            <ContainerFileTree
              entriesByDirectory={entriesByDirectory}
              expandedPaths={expandedPaths}
              loadingPaths={loadingPaths}
              onSelect={(entry) => setSelectedPath(entry.path)}
              onToggle={toggleDirectory}
              root={currentRoot}
              selectedPath={selectedPath}
            />
          ) : (
            <div className="grid h-full place-items-center text-[10px] text-muted-foreground">
              Loading {currentRoot}…
            </div>
          )}
        </div>
        <div className="min-w-0 p-4">
          {selected ? (
            <>
              <div className="flex items-start gap-3 border-b border-border pb-4">
                <div className="min-w-0 flex-1">
                  <p className="font-mono text-[10px] break-all">
                    {selected.path}
                  </p>
                  <p className="mt-1 text-[9px] text-muted-foreground">
                    {selected.directory
                      ? "Directory"
                      : formatBytes(selected.sizeBytes)}
                    {" · "}
                    {new Date(selected.modifiedAt).toLocaleString()}
                  </p>
                </div>
                {selected.directory ? (
                  <Button
                    disabled={busy}
                    onClick={() => void loadDirectory(selected.path, true)}
                    size="sm"
                    variant="outline"
                  >
                    Open as root
                  </Button>
                ) : (
                  <a
                    className="inline-flex h-7 shrink-0 items-center justify-center gap-1.5 border border-border bg-background px-2 text-xs font-medium text-foreground transition-colors hover:bg-muted active:translate-y-px [&_svg]:size-3.5"
                    href={containerFileContentURL(
                      projectID,
                      resourceKind,
                      resourceID,
                      selected.path
                    )}
                  >
                    <Download />
                    Download
                  </a>
                )}
              </div>
              <dl className="mt-3 grid grid-cols-[7rem_minmax(0,1fr)] gap-y-2 text-[9px]">
                <dt className="text-muted-foreground">Permissions</dt>
                <dd className="font-mono">
                  {selected.mode.toString(8).padStart(3, "0")}
                </dd>
                <dt className="text-muted-foreground">Scope</dt>
                <dd>Current running container</dd>
              </dl>
            </>
          ) : (
            <div className="grid h-full place-items-center px-8 text-center text-[10px] leading-5 text-muted-foreground">
              Expand directories as needed. Select a file to inspect or download
              it.
            </div>
          )}
        </div>
      </div>
    </SectionCard>
  );
};
