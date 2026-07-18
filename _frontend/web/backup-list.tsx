import {
  ArchiveRestore,
  CheckCircle2,
  CircleAlert,
  LoaderCircle,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import type { BackupGeneration, BackupRecord } from "@/api";
import {
  formatBackupBytes,
  formatBackupDuration,
  formatBackupTimestamp,
} from "@/backup-format";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { cn } from "@/lib/utils";

interface BackupListItem {
  generation?: BackupGeneration;
  key: string;
  record?: BackupRecord;
  timestamp: number;
}

const backupStates: Record<
  BackupRecord["status"],
  { className: string; icon: LucideIcon; label: string }
> = {
  failed: {
    className: "text-destructive",
    icon: CircleAlert,
    label: "Backup failed",
  },
  interrupted: {
    className: "text-destructive",
    icon: CircleAlert,
    label: "Backup interrupted",
  },
  running: {
    className: "text-amber-600",
    icon: LoaderCircle,
    label: "Backup in progress",
  },
  succeeded: {
    className: "text-emerald-600",
    icon: CheckCircle2,
    label: "Backup complete",
  },
};

export const recentBackupItems = (
  history: BackupRecord[],
  generations: BackupGeneration[]
): BackupListItem[] => {
  const generationsByID = new Map(
    generations.map((generation) => [generation.generationId, generation])
  );
  const representedGenerations = new Set<string>();
  const items = history.map((record): BackupListItem => {
    const generation = record.generationId
      ? generationsByID.get(record.generationId)
      : undefined;
    if (generation) {
      representedGenerations.add(generation.generationId);
    }
    return {
      generation,
      key: `run-${record.id}`,
      record,
      timestamp: record.finishedAt ?? record.startedAt,
    };
  });

  for (const generation of generations) {
    if (!representedGenerations.has(generation.generationId)) {
      items.push({
        generation,
        key: `generation-${generation.generationId}`,
        timestamp: generation.completedAt,
      });
    }
  }
  return items.toSorted((left, right) => right.timestamp - left.timestamp);
};

const BackupState = ({ record }: { record?: BackupRecord }) => {
  const status = record?.status ?? "succeeded";
  const state = backupStates[status];
  const Icon = state.icon;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 text-[10px]",
        state.className
      )}
    >
      <Icon
        className={cn("size-3.5", status === "running" && "animate-spin")}
      />
      {state.label}
    </span>
  );
};

const backupOrigin = (record?: BackupRecord) => {
  if (!record) {
    return "Older backup";
  }
  return record.scheduledOccurrence ? "Scheduled" : "Manual";
};

export const BackupList = ({
  generations,
  history,
  onRestore,
  restoring,
}: {
  generations: BackupGeneration[];
  history: BackupRecord[];
  onRestore: (generation: BackupGeneration) => void;
  restoring: boolean;
}) => {
  const items = recentBackupItems(history, generations);
  return (
    <SectionCard>
      <header className="flex items-center justify-between gap-3 border-b border-border px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">Recent backups</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Completed, running, and failed backup attempts.
          </p>
        </div>
        <span className="text-[9px] text-muted-foreground">
          {items.length} {items.length === 1 ? "backup" : "backups"}
        </span>
      </header>

      {items.length === 0 ? (
        <p className="px-5 py-5 text-[9px] text-muted-foreground">
          No backups yet.
        </p>
      ) : (
        items.map(({ generation, key, record, timestamp }) => {
          const duration = record ? formatBackupDuration(record) : undefined;
          const size = generation?.remoteSize ?? record?.sizeBytes;
          return (
            <div
              className="grid items-center gap-3 border-b border-border px-5 py-3 last:border-b-0 md:grid-cols-[minmax(12rem,1fr)_minmax(10rem,0.8fr)_7rem_auto]"
              key={key}
            >
              <div className="min-w-0">
                <BackupState record={record} />
                <p className="mt-1 text-[8px] text-muted-foreground">
                  {backupOrigin(record)}
                  {duration ? ` · ${duration}` : ""}
                </p>
                {record?.errorMessage || record?.errorCode ? (
                  <p className="mt-1 truncate text-[8px] text-destructive">
                    {record.errorMessage ?? record.errorCode}
                  </p>
                ) : null}
              </div>
              <time className="text-[9px] text-muted-foreground">
                {formatBackupTimestamp(timestamp)}
              </time>
              <span className="text-right text-[9px] text-muted-foreground">
                {formatBackupBytes(size)}
              </span>
              {generation ? (
                <Button
                  disabled={restoring}
                  onClick={() => onRestore(generation)}
                  size="sm"
                  variant="ghost"
                >
                  <ArchiveRestore /> Restore
                </Button>
              ) : (
                <span />
              )}
            </div>
          );
        })
      )}
    </SectionCard>
  );
};
