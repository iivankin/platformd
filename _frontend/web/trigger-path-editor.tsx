import { Folder, Plus, X } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { RepositoryPathCombobox } from "@/repository-path-combobox";
import {
  addTriggerPath,
  isTriggerPathCovered,
  normalizeTriggerPath,
} from "@/trigger-path-model";

export const TriggerPathEditor = ({
  branch,
  onChange,
  paths,
  repositoryID,
}: {
  branch: string;
  onChange: (paths: string[]) => void;
  paths: string[];
  repositoryID: number;
}) => {
  const [candidate, setCandidate] = useState("");
  const normalizedCandidate = normalizeTriggerPath(candidate);
  const candidateIsCovered = isTriggerPathCovered(normalizedCandidate, paths);

  const addPath = (value: string) => {
    const nextPaths = addTriggerPath(paths, value);
    if (nextPaths !== paths) {
      onChange(nextPaths);
    }
    setCandidate("");
  };

  return (
    <div className="grid gap-2 md:col-span-2">
      <div>
        <p className="text-[9px] text-muted-foreground">Trigger paths</p>
        <p className="mt-1 text-[8px] leading-relaxed text-muted-foreground/75">
          Deploy only when a changed file is inside one of these paths. Leave
          empty to deploy every push to the selected branch.
        </p>
      </div>
      {paths.length > 0 ? (
        <div className="border border-border">
          {paths.map((path, index) => (
            <div
              className="flex h-8 items-center gap-2 border-b border-border px-2.5 text-[9px] last:border-b-0"
              key={path}
            >
              <Folder className="size-3 text-muted-foreground" />
              <code className="min-w-0 flex-1 truncate">{path}</code>
              <Button
                aria-label={`Remove trigger path ${path}`}
                onClick={() =>
                  onChange(paths.filter((_, itemIndex) => itemIndex !== index))
                }
                size="icon"
                type="button"
                variant="ghost"
              >
                <X />
              </Button>
            </div>
          ))}
        </div>
      ) : (
        <div className="border border-dashed border-border px-3 py-2 text-[8px] text-muted-foreground">
          Every changed path can trigger a deployment.
        </div>
      )}
      <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
        <RepositoryPathCombobox
          branch={branch}
          excludedPaths={paths}
          id="service-source-trigger-paths"
          kind="path"
          onChange={setCandidate}
          onSelect={addPath}
          placeholder="Select or enter a repository path"
          repositoryID={repositoryID}
          value={candidate}
        />
        <Button
          disabled={!normalizedCandidate || candidateIsCovered}
          onClick={() => addPath(candidate)}
          type="button"
          variant="outline"
        >
          <Plus />
          Add path
        </Button>
      </div>
    </div>
  );
};
