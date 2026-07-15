import {
  FileTree,
  useFileTree,
  useFileTreeSelection,
} from "@pierre/trees/react";
import { Download, FolderTree, RefreshCw, Upload } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties, FormEvent } from "react";

import {
  containerFileContentURL,
  fetchContainerFiles,
  uploadContainerFile,
} from "@/api";
import type { ContainerFileEntry, ContainerResourceKind } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface ContainerFileBrowserProperties {
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

const treePath = (root: string, entry: ContainerFileEntry) => {
  const prefix = root === "/" ? "/" : `${root}/`;
  const relative = entry.path.startsWith(prefix)
    ? entry.path.slice(prefix.length)
    : entry.path.replace(/^\/+/u, "");
  return entry.directory ? `${relative}/` : relative;
};

const absolutePath = (root: string, relative: string) => {
  const clean = relative.endsWith("/") ? relative.slice(0, -1) : relative;
  return root === "/" ? `/${clean}` : `${root}/${clean}`;
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
  projectID,
  resourceID,
  resourceKind,
}: ContainerFileBrowserProperties) => {
  const [root, setRoot] = useState("/");
  const [requestedRoot, setRequestedRoot] = useState("/");
  const [entries, setEntries] = useState<ContainerFileEntry[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const uploadRef = useRef<HTMLInputElement>(null);
  const paths = useMemo(
    () => entries.map((entry) => treePath(requestedRoot, entry)),
    [entries, requestedRoot]
  );
  const { model } = useFileTree({
    density: "compact",
    flattenEmptyDirectories: false,
    initialExpansion: 1,
    paths: [],
    search: true,
  });
  const selection = useFileTreeSelection(model);

  useEffect(() => {
    model.resetPaths(paths);
  }, [model, paths]);

  const load = useCallback(
    async (path: string, signal?: AbortSignal) => {
      setBusy(true);
      try {
        const tree = await fetchContainerFiles(
          projectID,
          resourceKind,
          resourceID,
          path,
          signal
        );
        setEntries(tree.entries);
        setRequestedRoot(tree.root);
        setRoot(tree.root);
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
        setBusy(false);
      }
    },
    [projectID, resourceID, resourceKind]
  );

  useEffect(() => {
    const controller = new AbortController();
    const loadInitialTree = async () => {
      try {
        const tree = await fetchContainerFiles(
          projectID,
          resourceKind,
          resourceID,
          "/",
          controller.signal
        );
        setEntries(tree.entries);
        setRequestedRoot(tree.root);
        setRoot(tree.root);
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
        setBusy(false);
      }
    };
    void loadInitialTree();
    return () => controller.abort();
  }, [projectID, resourceID, resourceKind]);

  const selectedRelative = selection.at(-1);
  const selected = selectedRelative
    ? entries.find(
        (entry) => entry.path === absolutePath(requestedRoot, selectedRelative)
      )
    : undefined;

  const submitRoot = (event: FormEvent) => {
    event.preventDefault();
    if (!root.startsWith("/") || root.includes("\0")) {
      setError("Path must be absolute");
      return;
    }
    void load(root);
  };

  const upload = async (file: File) => {
    if (file.name.includes("/")) {
      setError("File name is invalid");
      return;
    }
    const directory = uploadDirectory(selected, requestedRoot);
    setBusy(true);
    try {
      await uploadContainerFile(
        projectID,
        resourceKind,
        resourceID,
        joinPath(directory, file.name),
        file
      );
      await load(requestedRoot);
    } catch (uploadError) {
      setError(
        uploadError instanceof Error
          ? uploadError.message
          : "Unable to upload file"
      );
    } finally {
      setBusy(false);
    }
  };

  const treeStyles = {
    "--trees-bg-muted-override": "var(--muted)",
    "--trees-bg-override": "var(--background)",
    "--trees-border-color-override": "var(--border)",
    "--trees-border-radius-override": "0px",
    "--trees-fg-muted-override": "var(--muted-foreground)",
    "--trees-fg-override": "var(--foreground)",
    "--trees-font-family-override": "var(--font-sans)",
    "--trees-font-size-override": "10px",
    "--trees-input-bg-override": "var(--background)",
    "--trees-search-bg-override": "var(--background)",
    "--trees-selected-bg-override": "var(--muted)",
    height: "100%",
  } as CSSProperties;

  return (
    <section className="border-t border-border bg-background">
      <header className="flex min-h-11 flex-wrap items-center gap-2 border-b border-border px-4 py-2">
        <FolderTree className="size-4 text-muted-foreground" />
        <div>
          <h3 className="text-[10px] font-medium">Container files</h3>
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Live filesystem · Trees
          </p>
        </div>
        <form className="ml-4 flex min-w-64 flex-1 gap-2" onSubmit={submitRoot}>
          <Input
            aria-label="Container root path"
            className="h-8 font-mono text-[10px]"
            onChange={(event) => setRoot(event.target.value)}
            value={root}
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

      <div className="grid h-[24rem] grid-cols-[minmax(16rem,0.9fr)_minmax(15rem,1.1fr)]">
        <div className="min-w-0 border-r border-border p-2">
          <FileTree
            aria-label={`Files below ${requestedRoot}`}
            model={model}
            style={treeStyles}
          />
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
                    onClick={() => void load(selected.path)}
                    size="sm"
                    variant="outline"
                  >
                    Open directory
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
              Select a file to inspect or download it. Uploads go to the
              selected directory.
            </div>
          )}
        </div>
      </div>
    </section>
  );
};
