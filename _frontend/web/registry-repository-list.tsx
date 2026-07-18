import { LockKeyhole, PackageOpen, Radio } from "lucide-react";

import type { RegistryRepository } from "@/api";
import { SectionCard } from "@/components/ui/card";
import { cn } from "@/lib/utils";

const formatBytes = (bytes: number) => {
  if (bytes < 1024) {
    return `${bytes.toString()} B`;
  }
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(value < 10 ? 1 : 0)} ${units[unitIndex]}`;
};

export const RegistryRepositoryList = ({
  onSelect,
  repositories,
}: {
  onSelect: (repository: RegistryRepository) => void;
  repositories: RegistryRepository[];
}) => (
  <SectionCard className="min-h-0 overflow-auto">
    <div className="sticky top-0 z-10 grid grid-cols-[minmax(0,1fr)_8rem_8rem] border-b border-border bg-muted/25 px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
      <span>Repository</span>
      <span>Access</span>
      <span>Storage</span>
    </div>
    {repositories.map((repository) => {
      const AccessIcon = repository.publicPull ? Radio : LockKeyhole;
      return (
        <button
          className={cn(
            "grid w-full grid-cols-[minmax(0,1fr)_8rem_8rem] items-center border-b border-border px-5 py-4 text-left transition-colors hover:bg-muted/40"
          )}
          key={repository.id}
          onClick={() => onSelect(repository)}
          type="button"
        >
          <span className="min-w-0">
            <span className="flex items-center gap-2">
              <PackageOpen className="size-3.5 shrink-0 text-muted-foreground" />
              <span className="truncate font-mono text-[10px] font-medium">
                {repository.name}
              </span>
            </span>
            <span className="mt-2 flex items-center gap-3 text-[9px] text-muted-foreground">
              <span>{repository.manifestCount} manifests</span>
              <span>{repository.tagCount} tags</span>
            </span>
          </span>
          <span className="inline-flex items-center gap-1 text-[9px] text-muted-foreground">
            <AccessIcon className="size-2.5" />
            {repository.publicPull ? "Public" : "Private"}
          </span>
          <span className="text-[9px] text-muted-foreground tabular-nums">
            {formatBytes(repository.totalBlobBytes)}
          </span>
        </button>
      );
    })}
    {repositories.length === 0 ? (
      <div className="grid min-h-64 place-items-center px-8 text-center">
        <div>
          <PackageOpen className="mx-auto size-6 text-muted-foreground" />
          <p className="mt-4 text-xs font-medium">No repositories</p>
          <p className="mt-2 text-[10px] leading-5 text-muted-foreground">
            Create one to receive an OCI push endpoint and a robot credential.
          </p>
        </div>
      </div>
    ) : null}
  </SectionCard>
);

export { formatBytes as formatRegistryBytes };
