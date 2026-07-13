import { ChevronDown } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  fetchBackupGenerations,
  fetchBackupHistory,
  runBackupNow,
  setBackupPolicy,
} from "@/api";
import type {
  BackupGeneration,
  BackupPolicy,
  BackupRecord,
  RecoveryResourceKind,
} from "@/api";
import { BackupResourceDetails } from "@/backup-resource-details";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { useResourceRestore } from "@/use-resource-restore";

const historyPollMilliseconds = 2000;

const resourceLabel: Record<RecoveryResourceKind, string> = {
  object_store: "Object Store",
  postgres: "PostgreSQL",
  redis: "Redis",
  registry: "Registry",
};

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

interface BackupResourceRowProperties {
  onPolicyUpdated: (policy: BackupPolicy) => void;
  policy: BackupPolicy;
}

export const BackupResourceRow = ({
  onPolicyUpdated,
  policy,
}: BackupResourceRowProperties) => {
  const [expanded, setExpanded] = useState(false);
  const [enabled, setEnabled] = useState(policy.enabled);
  const [cron, setCron] = useState(policy.cron ?? "0 3 * * *");
  const [retentionCount, setRetentionCount] = useState(
    String(policy.retentionCount)
  );
  const [busy, setBusy] = useState("");
  const [error, setError] = useState<string>();
  const [history, setHistory] = useState<BackupRecord[]>([]);
  const [generations, setGenerations] = useState<BackupGeneration[]>([]);
  const [detailsLoaded, setDetailsLoaded] = useState(false);
  const [detailsLoading, setDetailsLoading] = useState(false);
  const [selected, setSelected] = useState<BackupGeneration>();
  const detailsInFlight = useRef(false);
  const detailsController = useRef<AbortController | null>(null);

  useEffect(() => () => detailsController.current?.abort(), []);

  const loadDetails = useCallback(
    async (signal?: AbortSignal) => {
      if (detailsInFlight.current) {
        return;
      }
      detailsInFlight.current = true;
      setDetailsLoading(true);
      try {
        const [loadedHistory, loadedGenerations] = await Promise.all([
          fetchBackupHistory(policy.resourceKind, policy.resourceId, signal),
          fetchBackupGenerations(
            policy.resourceKind,
            policy.resourceId,
            signal
          ),
        ]);
        setHistory(loadedHistory);
        setGenerations(loadedGenerations);
        setDetailsLoaded(true);
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load resource backups"));
        }
      } finally {
        detailsInFlight.current = false;
        if (!signal?.aborted) {
          setDetailsLoading(false);
        }
      }
    },
    [policy.resourceId, policy.resourceKind]
  );

  const afterRestore = useCallback(async () => {
    setSelected(undefined);
    await loadDetails();
  }, [loadDetails]);
  const restore = useResourceRestore({
    onSucceeded: afterRestore,
    resourceID: policy.resourceId,
    resourceKind: policy.resourceKind,
  });

  const toggle = async () => {
    if (expanded) {
      setExpanded(false);
      return;
    }
    setExpanded(true);
    if (detailsLoaded || detailsLoading) {
      return;
    }
    detailsController.current?.abort();
    const controller = new AbortController();
    detailsController.current = controller;
    await loadDetails(controller.signal);
  };

  const refresh = async () => {
    detailsController.current?.abort();
    const controller = new AbortController();
    detailsController.current = controller;
    await loadDetails(controller.signal);
  };

  const save = async () => {
    const retention = Math.trunc(Number(retentionCount));
    if (
      busy ||
      !Number.isInteger(retention) ||
      retention < 1 ||
      retention > 100
    ) {
      return;
    }
    setBusy("save");
    setError(undefined);
    try {
      const updated = await setBackupPolicy(
        policy.resourceKind,
        policy.resourceId,
        { cron: enabled ? cron.trim() : "", enabled, retentionCount: retention }
      );
      setEnabled(updated.enabled);
      setCron(updated.cron ?? cron);
      setRetentionCount(String(updated.retentionCount));
      onPolicyUpdated(updated);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to update backup policy"));
    } finally {
      setBusy("");
    }
  };

  const run = async () => {
    if (busy) {
      return;
    }
    setBusy("run");
    setError(undefined);
    try {
      const record = await runBackupNow(policy.resourceKind, policy.resourceId);
      setHistory((current) => [
        record,
        ...current.filter((entry) => entry.id !== record.id),
      ]);
      setDetailsLoaded(true);
      setExpanded(true);
    } catch (runError) {
      setError(errorText(runError, "Unable to start backup"));
    } finally {
      setBusy("");
    }
  };

  const hasRunningBackup = history.some(
    (record) => record.status === "running"
  );
  useEffect(() => {
    if (!expanded || !hasRunningBackup) {
      return;
    }
    const controller = new AbortController();
    const interval = window.setInterval(
      () => void loadDetails(controller.signal),
      historyPollMilliseconds
    );
    return () => {
      controller.abort();
      window.clearInterval(interval);
    };
  }, [expanded, hasRunningBackup, loadDetails]);

  const retention = Math.trunc(Number(retentionCount));
  const policyValid =
    !busy &&
    Number.isInteger(retention) &&
    retention >= 1 &&
    retention <= 100 &&
    (!enabled || Boolean(cron.trim()));
  const visibleError = error || restore.error;

  return (
    <div className="border-b border-border last:border-b-0">
      <div className="grid items-center gap-3 px-5 py-4 md:grid-cols-[minmax(12rem,1fr)_minmax(12rem,1fr)_minmax(9rem,0.65fr)_auto]">
        <div className="min-w-0">
          <p className="text-[10px] font-medium">
            {resourceLabel[policy.resourceKind]}
          </p>
          <p className="mt-1 truncate font-mono text-[9px] text-muted-foreground">
            {policy.resourceId}
          </p>
        </div>
        <div className="min-w-0">
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Schedule
          </p>
          <p className="mt-1.5 truncate font-mono text-[9px]">
            {policy.enabled ? `${policy.cron} UTC` : "Disabled"}
          </p>
        </div>
        <div>
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Retention
          </p>
          <p className="mt-1.5 text-[10px]">
            {policy.retentionCount} generations
          </p>
        </div>
        <Button onClick={() => void toggle()} size="sm" variant="outline">
          <ChevronDown
            className={cn("transition-transform", expanded && "rotate-180")}
          />
          {expanded ? "Close" : "Manage"}
        </Button>
      </div>

      {expanded ? (
        <BackupResourceDetails
          busy={busy}
          cron={cron}
          detailsLoading={detailsLoading}
          enabled={enabled}
          error={visibleError}
          generations={generations}
          history={history}
          onCronChange={setCron}
          onEnabledChange={setEnabled}
          onRefresh={() => void refresh()}
          onRestore={(generationID) => void restore.start(generationID)}
          onRetentionChange={setRetentionCount}
          onRun={() => void run()}
          onSave={() => void save()}
          onSelectedChange={setSelected}
          policy={policy}
          policyValid={policyValid}
          restoring={restore.restoring}
          restoreProgress={restore.operation?.progress}
          retentionCount={retentionCount}
          selected={selected}
        />
      ) : null}
    </div>
  );
};
