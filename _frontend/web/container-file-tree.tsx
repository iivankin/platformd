import {
  ChevronRight,
  File,
  Folder,
  FolderOpen,
  LoaderCircle,
} from "lucide-react";
import { useMemo } from "react";

import type { ContainerFileEntry } from "@/api";
import { cn } from "@/lib/utils";

interface ContainerFileTreeProperties {
  entriesByDirectory: ReadonlyMap<string, readonly ContainerFileEntry[]>;
  expandedPaths: ReadonlySet<string>;
  loadingPaths: ReadonlySet<string>;
  onSelect: (entry: ContainerFileEntry) => void;
  onToggle: (entry: ContainerFileEntry) => void;
  root: string;
  selectedPath?: string;
}

interface TreeBranchProperties extends Omit<
  ContainerFileTreeProperties,
  "entriesByDirectory"
> {
  depth: number;
  sortedEntries: ReadonlyMap<string, readonly ContainerFileEntry[]>;
}

const sortEntries = (entries: readonly ContainerFileEntry[]) =>
  entries.toSorted((left, right) => {
    if (left.directory !== right.directory) {
      return left.directory ? -1 : 1;
    }
    return left.path.localeCompare(right.path);
  });

const entryName = (path: string) => path.split("/").at(-1) ?? path;

const EntryIcon = ({
  directory,
  expanded,
}: {
  directory: boolean;
  expanded: boolean;
}) => {
  if (!directory) {
    return <File className="size-3.5 shrink-0 text-muted-foreground" />;
  }
  if (expanded) {
    return <FolderOpen className="size-3.5 shrink-0 text-muted-foreground" />;
  }
  return <Folder className="size-3.5 shrink-0 text-muted-foreground" />;
};

const TreeBranch = ({
  depth,
  expandedPaths,
  loadingPaths,
  onSelect,
  onToggle,
  root,
  selectedPath,
  sortedEntries,
}: TreeBranchProperties) => {
  const entries = sortedEntries.get(root) ?? [];
  if (entries.length === 0) {
    return (
      <div
        className="px-3 py-2 text-[9px] text-muted-foreground"
        style={{ paddingLeft: `${depth * 16 + 12}px` }}
      >
        Empty directory
      </div>
    );
  }

  return entries.map((entry) => {
    const expanded = entry.directory && expandedPaths.has(entry.path);
    const loading = entry.directory && loadingPaths.has(entry.path);
    const selected = selectedPath === entry.path;
    return (
      <div key={entry.path}>
        <div
          aria-expanded={entry.directory ? expanded : undefined}
          aria-level={depth + 1}
          aria-selected={selected}
          className={cn(
            "group flex h-7 items-center pr-2 text-[10px]",
            selected ? "bg-muted text-foreground" : "hover:bg-muted/55"
          )}
          role="treeitem"
          style={{ paddingLeft: `${depth * 16 + 6}px` }}
        >
          {entry.directory ? (
            <button
              aria-label={`${expanded ? "Collapse" : "Expand"} ${entryName(entry.path)}`}
              className="grid size-6 shrink-0 place-items-center text-muted-foreground hover:text-foreground"
              disabled={loading}
              onClick={() => onToggle(entry)}
              type="button"
            >
              {loading ? (
                <LoaderCircle className="size-3 animate-spin" />
              ) : (
                <ChevronRight
                  className={cn(
                    "size-3 transition-transform duration-100",
                    expanded && "rotate-90"
                  )}
                />
              )}
            </button>
          ) : (
            <span className="size-6 shrink-0" />
          )}
          <button
            className="flex min-w-0 flex-1 items-center gap-2 self-stretch text-left outline-none"
            onClick={() => onSelect(entry)}
            onDoubleClick={() => {
              if (entry.directory) {
                onToggle(entry);
              }
            }}
            type="button"
          >
            <EntryIcon directory={entry.directory} expanded={expanded} />
            <span className="truncate">{entryName(entry.path)}</span>
          </button>
        </div>
        {expanded ? (
          <fieldset className="m-0 border-0 p-0">
            <TreeBranch
              depth={depth + 1}
              expandedPaths={expandedPaths}
              loadingPaths={loadingPaths}
              onSelect={onSelect}
              onToggle={onToggle}
              root={entry.path}
              selectedPath={selectedPath}
              sortedEntries={sortedEntries}
            />
          </fieldset>
        ) : null}
      </div>
    );
  });
};

export const ContainerFileTree = (properties: ContainerFileTreeProperties) => {
  const sortedEntries = useMemo(() => {
    const result = new Map<string, readonly ContainerFileEntry[]>();
    for (const [directory, entries] of properties.entriesByDirectory) {
      result.set(directory, sortEntries(entries));
    }
    return result;
  }, [properties.entriesByDirectory]);

  return (
    <div
      aria-label={`Files below ${properties.root}`}
      className="h-full overflow-auto py-1"
      role="tree"
    >
      <TreeBranch {...properties} depth={0} sortedEntries={sortedEntries} />
    </div>
  );
};
