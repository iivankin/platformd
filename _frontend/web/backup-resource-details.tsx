import {
  ArchiveRestore,
  Check,
  Clock3,
  LoaderCircle,
  Play,
  RefreshCw,
  Save,
} from "lucide-react";

import type { BackupGeneration, BackupPolicy, BackupRecord } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const bytes = (value?: number) => {
  if (value === undefined) {
    return "—";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

const timestamp = (value?: number) =>
  value
    ? new Date(value).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : "—";

const duration = (record?: BackupRecord) => {
  if (!record?.finishedAt || record.finishedAt < record.startedAt) {
    return "—";
  }
  const seconds = Math.round((record.finishedAt - record.startedAt) / 1000);
  return seconds < 60
    ? `${seconds}s`
    : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
};

const BackupRunSummary = ({
  history,
  policy,
}: {
  history: BackupRecord[];
  policy: BackupPolicy;
}) => {
  const lastSuccess = history.find((record) => record.status === "succeeded");
  const lastFailure = history.find(
    (record) => record.status === "failed" || record.status === "interrupted"
  );
  const next = policy.nextRunAt ? new Date(policy.nextRunAt) : undefined;
  return (
    <div className="grid border-b border-border text-[9px] md:grid-cols-3">
      <div className="border-b border-border px-5 py-3 md:border-r md:border-b-0">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Next run
        </p>
        <p className="mt-1">
          {next ? next.toISOString().replace(".000Z", "Z") : "Disabled"}
        </p>
        <p className="mt-1 text-muted-foreground">
          {next ? `${next.toLocaleString()} local` : "No scheduled occurrence"}
        </p>
      </div>
      <div className="border-b border-border px-5 py-3 md:border-r md:border-b-0">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Last success
        </p>
        <p className="mt-1">{timestamp(lastSuccess?.finishedAt)}</p>
        <p className="mt-1 text-muted-foreground">
          {duration(lastSuccess)} · {bytes(lastSuccess?.sizeBytes)}
        </p>
      </div>
      <div className="px-5 py-3">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Last error
        </p>
        <p className="mt-1 text-rose-600 dark:text-rose-300">
          {lastFailure?.errorMessage || lastFailure?.errorCode || "—"}
        </p>
        <p className="mt-1 text-muted-foreground">
          {timestamp(lastFailure?.finishedAt)}
        </p>
      </div>
    </div>
  );
};

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
  policy: BackupPolicy;
  policyValid: boolean;
  restoring: boolean;
  restoreProgress?: string;
  retentionCount: string;
  selected?: BackupGeneration;
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
  policy,
  policyValid,
  restoring,
  restoreProgress,
  retentionCount,
  selected,
}: BackupResourceDetailsProperties) => (
  <div>
    <div className="grid border-b border-border md:grid-cols-[9rem_minmax(16rem,1fr)_10rem_auto]">
      <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Scheduled
        </p>
        <Button
          className="mt-2 w-full"
          onClick={() => onEnabledChange(!enabled)}
          size="sm"
          variant={enabled ? "default" : "outline"}
        >
          {enabled ? <Check /> : <Clock3 />}
          {enabled ? "Enabled" : "Disabled"}
        </Button>
      </div>
      <label
        className="border-b border-border px-5 py-4 md:border-r md:border-b-0"
        htmlFor={`backup-cron-${policy.resourceId}`}
      >
        <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Five-field UTC cron
        </span>
        <Input
          className="mt-2 font-mono"
          disabled={!enabled}
          id={`backup-cron-${policy.resourceId}`}
          onChange={(event) => onCronChange(event.target.value)}
          value={cron}
        />
      </label>
      <label
        className="border-b border-border px-5 py-4 md:border-r md:border-b-0"
        htmlFor={`backup-retention-${policy.resourceId}`}
      >
        <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Keep generations
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
      <div className="flex items-end gap-2 px-5 py-4">
        <Button disabled={!policyValid} onClick={onSave} size="sm">
          {busy === "save" ? (
            <LoaderCircle className="animate-spin" />
          ) : (
            <Save />
          )}
          Save policy
        </Button>
      </div>
    </div>

    <BackupRunSummary history={history} policy={policy} />

    <div className="flex flex-wrap items-center gap-2 border-b border-border px-5 py-3">
      <Button disabled={Boolean(busy)} onClick={onRun} size="sm">
        {busy === "run" ? <LoaderCircle className="animate-spin" /> : <Play />}
        Backup now
      </Button>
      <Button
        disabled={detailsLoading}
        onClick={onRefresh}
        size="sm"
        variant="ghost"
      >
        <RefreshCw className={cn(detailsLoading && "animate-spin")} />
        Refresh
      </Button>
      <span className="ml-auto text-[9px] text-muted-foreground">
        Manual backup works even while the schedule is disabled.
      </span>
    </div>

    <div className="grid xl:grid-cols-2">
      <section className="border-b border-border xl:border-r xl:border-b-0">
        <div className="border-b border-border px-5 py-3 text-[9px] font-medium">
          Complete generations · {generations.length}
        </div>
        {generations.length === 0 ? (
          <p className="px-5 py-4 text-[9px] text-muted-foreground">
            No complete remote generation.
          </p>
        ) : (
          generations.map((generation) => (
            <div
              className="grid items-center gap-2 border-b border-border px-5 py-3 last:border-b-0 md:grid-cols-[minmax(10rem,1fr)_minmax(9rem,1fr)_auto]"
              key={generation.generationId}
            >
              <div className="min-w-0">
                <p className="truncate font-mono text-[9px]">
                  {generation.generationId}
                </p>
                <p className="mt-1 text-[8px] text-muted-foreground">
                  {bytes(generation.plaintextSize)} raw ·{" "}
                  {bytes(generation.remoteSize)} remote
                </p>
              </div>
              <span className="text-[8px] text-muted-foreground">
                {timestamp(generation.completedAt)}
              </span>
              <Button
                disabled={restoring}
                onClick={() => onSelectedChange(generation)}
                size="sm"
                variant="ghost"
              >
                <ArchiveRestore />
                Restore
              </Button>
            </div>
          ))
        )}
      </section>

      <section>
        <div className="border-b border-border px-5 py-3 text-[9px] font-medium">
          Run history · latest 50
        </div>
        {history.length === 0 ? (
          <p className="px-5 py-4 text-[9px] text-muted-foreground">
            No backup runs yet.
          </p>
        ) : (
          history.map((record) => (
            <div
              className="grid gap-2 border-b border-border px-5 py-3 last:border-b-0 md:grid-cols-[7rem_minmax(9rem,1fr)_auto]"
              key={record.id}
            >
              <span
                className={cn(
                  "text-[9px] capitalize",
                  record.status === "succeeded" && "text-emerald-600",
                  record.status === "failed" && "text-rose-600",
                  record.status === "running" && "text-amber-600"
                )}
              >
                {record.status}
              </span>
              <div>
                <p className="text-[8px] text-muted-foreground">
                  {timestamp(record.startedAt)}
                </p>
                {record.errorMessage ? (
                  <p className="mt-1 text-[8px] text-rose-600">
                    {record.errorMessage}
                  </p>
                ) : null}
              </div>
              <span className="text-right text-[8px] text-muted-foreground">
                {bytes(record.sizeBytes)}
              </span>
            </div>
          ))
        )}
      </section>
    </div>

    {selected ? (
      <div className="flex flex-col gap-3 border-t border-amber-500/30 bg-amber-500/5 px-5 py-4 md:flex-row md:items-center">
        <ArchiveRestore className="size-4 shrink-0 text-amber-600" />
        <p className="min-w-0 flex-1 text-[10px] leading-4">
          Replace this resource with generation{" "}
          <span className="font-mono">{selected.generationId}</span>? Current
          data will be replaced after the remote generation is fully verified.
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
          Replace resource
        </Button>
      </div>
    ) : null}

    {restoring ? (
      <p className="border-t border-border px-5 py-3 text-[9px] text-muted-foreground">
        Restore operation · {restoreProgress || "starting"}
      </p>
    ) : null}
    {error ? (
      <p className="border-t border-rose-500/30 bg-rose-500/5 px-5 py-3 text-[10px] text-rose-600 dark:text-rose-300">
        {error}
      </p>
    ) : null}
  </div>
);
