import { Combobox } from "@base-ui/react/combobox";
import { File, Folder } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchGitHubRepositoryPaths } from "@/api";
import type { GitHubRepositoryPath } from "@/api";
import { isTriggerPathCovered } from "@/trigger-path-model";

const NO_EXCLUDED_PATHS: string[] = [];

export const RepositoryPathCombobox = ({
  branch,
  excludedPaths = NO_EXCLUDED_PATHS,
  id,
  kind,
  onChange,
  onSelect,
  placeholder,
  repositoryID,
  value,
}: {
  branch: string;
  excludedPaths?: string[];
  id: string;
  kind: "dockerfile" | "path";
  onChange: (value: string) => void;
  onSelect?: (value: string) => void;
  placeholder?: string;
  repositoryID: number;
  value: string;
}) => {
  const [items, setItems] = useState<GitHubRepositoryPath[]>([]);
  const query = value.trim();
  const visibleItems = items.filter(
    (item) => !isTriggerPathCovered(item.path, excludedPaths)
  );

  useEffect(() => {
    if (repositoryID <= 0 || !branch.trim()) {
      return;
    }
    const controller = new AbortController();
    const timer = setTimeout(async () => {
      try {
        const paths = await fetchGitHubRepositoryPaths(
          repositoryID,
          branch,
          query,
          kind,
          controller.signal
        );
        setItems(paths);
      } catch (error) {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          setItems([]);
        }
      }
    }, 150);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [branch, kind, query, repositoryID]);

  return (
    <Combobox.Root<GitHubRepositoryPath>
      autoHighlight
      disabled={repositoryID <= 0 || !branch.trim()}
      filter={() => true}
      inputValue={value}
      itemToStringLabel={(item) => item.path}
      items={visibleItems}
      onInputValueChange={(next, details) => {
        if (details.reason === "input-change") {
          onChange(next);
        }
      }}
      onValueChange={(item) => {
        if (item) {
          if (onSelect) {
            onSelect(item.path);
          } else {
            onChange(item.path);
          }
        }
      }}
      value={null}
    >
      <Combobox.Input
        autoCapitalize="none"
        autoComplete="off"
        className="h-8 w-full border border-input bg-background px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/55 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
        id={id}
        placeholder={placeholder}
        spellCheck={false}
      />
      <Combobox.Portal>
        <Combobox.Positioner align="start" className="z-50" sideOffset={4}>
          <Combobox.Popup className="max-h-64 w-[var(--anchor-width)] min-w-80 overflow-y-auto border border-border bg-popover p-1 text-popover-foreground shadow-lg">
            <Combobox.Empty className="px-3 py-4 text-[10px] text-muted-foreground empty:hidden">
              No matching repository paths.
            </Combobox.Empty>
            <Combobox.List>
              {(item: GitHubRepositoryPath) => {
                const Icon = item.type === "tree" ? Folder : File;
                return (
                  <Combobox.Item
                    className="flex cursor-default items-center gap-2 px-2.5 py-2 text-[10px] outline-none data-[highlighted]:bg-muted"
                    key={`${item.type}:${item.path}`}
                    value={item}
                  >
                    <Icon className="size-3 text-muted-foreground" />
                    <span className="truncate font-mono">{item.path}</span>
                  </Combobox.Item>
                );
              }}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  );
};
