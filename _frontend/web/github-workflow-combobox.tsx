import { Combobox } from "@base-ui/react/combobox";
import { Workflow } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchGitHubWorkflows } from "@/api";
import type { GitHubWorkflow } from "@/api";

export const GitHubWorkflowCombobox = ({
  id,
  onChange,
  repositoryID,
  value,
}: {
  id: string;
  onChange: (workflow?: GitHubWorkflow) => void;
  repositoryID: number;
  value?: GitHubWorkflow;
}) => {
  const [result, setResult] = useState<{
    failed: boolean;
    items: GitHubWorkflow[];
    repositoryID: number;
  }>({ failed: false, items: [], repositoryID: 0 });
  const current =
    result.repositoryID === repositoryID
      ? result
      : { failed: false, items: [], repositoryID };
  const loading = repositoryID > 0 && result.repositoryID !== repositoryID;

  useEffect(() => {
    if (repositoryID <= 0) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const workflows = await fetchGitHubWorkflows(
          repositoryID,
          controller.signal
        );
        setResult({ failed: false, items: workflows, repositoryID });
      } catch (error) {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          setResult({ failed: true, items: [], repositoryID });
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [repositoryID]);

  return (
    <Combobox.Root<GitHubWorkflow>
      autoHighlight
      disabled={repositoryID <= 0}
      itemToStringLabel={(workflow) => workflow.name}
      items={current.items}
      onValueChange={(workflow) => onChange(workflow ?? undefined)}
      value={value ?? null}
    >
      <Combobox.Input
        autoCapitalize="none"
        autoComplete="off"
        className="h-8 w-full border border-input bg-background px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/55 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
        id={id}
        placeholder={loading ? "Loading workflows…" : "Select workflow"}
        spellCheck={false}
      />
      <Combobox.Portal>
        <Combobox.Positioner align="start" className="z-50" sideOffset={4}>
          <Combobox.Popup className="max-h-64 w-[var(--anchor-width)] min-w-80 overflow-y-auto border border-border bg-popover p-1 text-popover-foreground shadow-lg">
            <Combobox.Empty className="px-3 py-4 text-[10px] text-muted-foreground empty:hidden">
              {current.failed
                ? "Unable to load GitHub workflows."
                : "No workflow_dispatch workflows found."}
            </Combobox.Empty>
            <Combobox.List>
              {(workflow: GitHubWorkflow) => (
                <Combobox.Item
                  className="grid cursor-default grid-cols-[auto_minmax(0,1fr)] items-center gap-x-2 px-2.5 py-2 text-[10px] outline-none data-[highlighted]:bg-muted"
                  key={workflow.path}
                  value={workflow}
                >
                  <Workflow className="row-span-2 size-3 text-muted-foreground" />
                  <span className="truncate">{workflow.name}</span>
                  <span className="truncate font-mono text-[8px] text-muted-foreground">
                    {workflow.path}
                  </span>
                </Combobox.Item>
              )}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  );
};
