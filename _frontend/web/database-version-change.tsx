import { RefreshCw } from "lucide-react";
import { useState } from "react";

import type { ManagedImageEngine } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { DatabaseVersionPreview } from "@/database-version-preview";
import { DatabaseVersionTagBrowser } from "@/database-version-tag-browser";
import { useDatabaseVersionChange } from "@/use-database-version-change";

interface DatabaseVersionChangeProperties {
  activeDigest: string;
  activeTag: string;
  engine: ManagedImageEngine;
  onSucceeded: () => Promise<void>;
  projectID: string;
  resourceID: string;
}

export const DatabaseVersionChange = ({
  activeDigest,
  activeTag,
  engine,
  onSucceeded,
  projectID,
  resourceID,
}: DatabaseVersionChangeProperties) => {
  const [open, setOpen] = useState(false);
  const change = useDatabaseVersionChange({
    engine,
    onSucceeded,
    projectID,
    resourceID,
  });
  const running = change.operation?.status === "running";

  const toggle = () => {
    const next = !open;
    setOpen(next);
    if (next && !change.tagPage && !change.tagsLoading) {
      void change.loadTags(1, "");
    }
  };

  return (
    <SectionCard className="shrink-0">
      <div className="flex items-center gap-3 px-4 py-3">
        <div className="min-w-0 flex-1">
          <h3 className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
            Database image
          </h3>
          <p className="mt-1 truncate font-mono text-[9px]">
            {engine}:{activeTag} · {activeDigest}
          </p>
        </div>
        <Button disabled={running} onClick={toggle} size="sm" variant="outline">
          <RefreshCw />
          {open ? "Close" : "Change version"}
        </Button>
      </div>

      {open ? (
        <div className="max-h-[34rem] overflow-y-auto border-t border-border bg-muted/10 px-4 py-4">
          <DatabaseVersionTagBrowser change={change} engine={engine} />
          <DatabaseVersionPreview change={change} />
          {change.operation ? (
            <p className="mt-3 border-t border-border pt-3 text-[9px] text-muted-foreground">
              Operation {change.operation.status} ·{" "}
              {change.operation.progress || "starting"}
            </p>
          ) : null}
          {change.error ? (
            <p aria-live="polite" className="mt-3 text-[9px] text-destructive">
              {change.error}
            </p>
          ) : null}
        </div>
      ) : null}
    </SectionCard>
  );
};
