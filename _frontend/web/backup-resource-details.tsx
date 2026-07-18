import {
  ArchiveRestore,
  Check,
  Clock3,
  LoaderCircle,
  Play,
  RefreshCw,
  Save,
} from "lucide-react";

import type {
  BackupGeneration,
  BackupPolicy,
  BackupRecord,
  BackupTarget,
} from "@/api";
import { formatBackupTimestamp } from "@/backup-format";
import { BackupList } from "@/backup-list";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

const noBackupStorage = "__no-backup-storage__";

interface BackupResourceDetailsProperties {
  busy: string;
  cron: string;
  detailsLoading: boolean;
  enabled: boolean;
  error?: string;
  generations: BackupGeneration[];
  history: BackupRecord[];
  onCronChange: (value: string) => void;
  onEnabledChange: (value: boolean) => void;
  onRefresh: () => void;
  onRestore: (generationID: string) => void;
  onRetentionChange: (value: string) => void;
  onRun: () => void;
  onSave: () => void;
  onSelectedChange: (generation?: BackupGeneration) => void;
  onTargetChange: (targetID: string) => void;
  policy: BackupPolicy;
  policyValid: boolean;
  restoring: boolean;
  restoreProgress?: string;
  retentionCount: string;
  selected?: BackupGeneration;
  targetID: string;
  targets: BackupTarget[];
}

export const BackupResourceDetails = ({
  busy,
  cron,
  detailsLoading,
  enabled,
  error,
  generations,
  history,
  onCronChange,
  onEnabledChange,
  onRefresh,
  onRestore,
  onRetentionChange,
  onRun,
  onSave,
  onSelectedChange,
  onTargetChange,
  policy,
  policyValid,
  restoring,
  restoreProgress,
  retentionCount,
  selected,
  targetID,
  targets,
}: BackupResourceDetailsProperties) => {
  const nextBackupVisible = Boolean(enabled && cron.trim() && policy.nextRunAt);

  return (
    <div className="grid gap-3">
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Backup schedule
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Run encrypted backups automatically and keep only the latest copies.
          </p>
        </div>
        <div className="border-t border-border lg:border-t-0 lg:border-l">
          <button
            aria-pressed={enabled}
            className="flex min-h-12 w-full items-center gap-3 border-b border-border px-5 text-left hover:bg-muted/40"
            onClick={() => onEnabledChange(!enabled)}
            type="button"
          >
            <span
              className={cn(
                "grid size-6 place-items-center border",
                enabled
                  ? "border-emerald-500/50 bg-emerald-500/10 text-emerald-600"
                  : "border-border text-muted-foreground"
              )}
            >
              {enabled ? (
                <Check className="size-3" />
              ) : (
                <Clock3 className="size-3" />
              )}
            </span>
            <span className="text-[10px]">Automatic backups</span>
            <span className="ml-auto text-[9px] text-muted-foreground">
              {enabled ? "On" : "Off"}
            </span>
          </button>

          <div className="grid md:grid-cols-[minmax(12rem,0.8fr)_minmax(14rem,1fr)_8rem_auto] md:items-end">
            <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
              <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Storage
              </span>
              <Select
                items={[
                  { label: "No storage", value: noBackupStorage },
                  ...targets.map((target) => ({
                    label: target.name,
                    value: target.id,
                  })),
                ]}
                onValueChange={(value) =>
                  onTargetChange(value === noBackupStorage ? "" : String(value))
                }
                value={targetID || noBackupStorage}
              >
                <SelectTrigger className="mt-2 h-9 w-full text-[10px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent align="start">
                  <SelectItem value={noBackupStorage}>No storage</SelectItem>
                  {targets.map((target) => (
                    <SelectItem key={target.id} value={target.id}>
                      {target.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <label
              className="border-b border-border px-5 py-4 md:border-r md:border-b-0"
              htmlFor={`backup-cron-${policy.resourceId}`}
            >
              <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Schedule · UTC cron
              </span>
              <Input
                className="mt-2 font-mono"
                id={`backup-cron-${policy.resourceId}`}
                onChange={(event) => onCronChange(event.target.value)}
                placeholder="0 3 * * *"
                value={cron}
              />
            </label>
            <label
              className="border-b border-border px-5 py-4 md:border-r md:border-b-0"
              htmlFor={`backup-retention-${policy.resourceId}`}
            >
              <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Keep latest
              </span>
              <Input
                className="mt-2"
                id={`backup-retention-${policy.resourceId}`}
                max={100}
                min={1}
                onChange={(event) => onRetentionChange(event.target.value)}
                type="number"
                value={retentionCount}
              />
            </label>
            <div className="px-5 py-4">
              <Button disabled={!policyValid} onClick={onSave} size="sm">
                {busy === "save" ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Save />
                )}
                Save schedule
              </Button>
            </div>
          </div>
        </div>
      </SectionCard>

      <SectionCard className="flex min-h-12 flex-wrap items-center gap-3 px-5 py-3">
        {policy.resourceKind === "volume" ? (
          <p className="w-full text-[9px] text-muted-foreground">
            Live file backup. The service stays online, so files changed while
            copying may be captured at different moments.
          </p>
        ) : null}
        {nextBackupVisible ? (
          <p className="inline-flex items-center gap-2 text-[10px]">
            <Clock3 className="size-3.5 text-muted-foreground" />
            <span className="text-muted-foreground">Next backup:</span>
            <time>{formatBackupTimestamp(policy.nextRunAt)}</time>
          </p>
        ) : null}
        <Button
          className={cn(!nextBackupVisible && "ml-auto")}
          disabled={Boolean(busy) || !targetID}
          onClick={onRun}
          size="sm"
        >
          {busy === "run" ? (
            <LoaderCircle className="animate-spin" />
          ) : (
            <Play />
          )}
          Back up now
        </Button>
        <Button
          aria-label="Refresh backups"
          className={cn(nextBackupVisible && "ml-auto")}
          disabled={detailsLoading}
          onClick={onRefresh}
          size="sm"
          variant="ghost"
        >
          <RefreshCw className={cn(detailsLoading && "animate-spin")} />
          Refresh
        </Button>
      </SectionCard>

      <BackupList
        generations={generations}
        history={history}
        onRestore={onSelectedChange}
        restoring={restoring}
      />

      {selected ? (
        <SectionCard className="flex flex-col gap-3 bg-amber-500/5 px-5 py-4 ring-amber-500/30 md:flex-row md:items-center">
          <ArchiveRestore className="size-4 shrink-0 text-amber-600" />
          <p className="min-w-0 flex-1 text-[10px] leading-4">
            Replace current data with the backup from{" "}
            <time>{formatBackupTimestamp(selected.completedAt)}</time>? The
            remote backup is verified before replacement starts.
          </p>
          <Button
            disabled={restoring}
            onClick={() => onSelectedChange()}
            size="sm"
            variant="ghost"
          >
            Cancel
          </Button>
          <Button
            disabled={restoring}
            onClick={() => onRestore(selected.generationId)}
            size="sm"
          >
            {restoring ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <ArchiveRestore />
            )}
            Restore backup
          </Button>
        </SectionCard>
      ) : null}

      {restoring ? (
        <SectionCard className="px-5 py-3 text-[9px] text-muted-foreground">
          Restore · {restoreProgress || "starting"}
        </SectionCard>
      ) : null}
      {error ? (
        <SectionCard className="bg-destructive/5 px-5 py-3 text-[10px] text-destructive ring-destructive/30">
          {error}
        </SectionCard>
      ) : null}
    </div>
  );
};
