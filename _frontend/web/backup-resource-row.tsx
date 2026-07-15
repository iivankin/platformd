import { useCallback, useEffect, useRef, useState } from "react";

import {
  fetchBackupGenerations,
  fetchBackupHistory,
  fetchBackupPolicy,
  runBackupNow,
  setBackupPolicy,
} from "@/api";
import type { BackupGeneration, BackupPolicy, BackupRecord } from "@/api";
import { BackupResourceDetails } from "@/backup-resource-details";
import { useResourceRestore } from "@/use-resource-restore";

const historyPollMilliseconds = 2000;

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

interface BackupResourceRowProperties {
  initialGenerations: BackupGeneration[];
  initialHistory: BackupRecord[];
  onPolicyUpdated: (policy: BackupPolicy) => void;
  policy: BackupPolicy;
}

export const BackupResourceRow = ({
  initialGenerations,
  initialHistory,
  onPolicyUpdated,
  policy,
}: BackupResourceRowProperties) => {
  const [enabled, setEnabled] = useState(policy.enabled);
  const [cron, setCron] = useState(policy.cron ?? "0 3 * * *");
  const [retentionCount, setRetentionCount] = useState(
    String(policy.retentionCount)
  );
  const [busy, setBusy] = useState("");
  const [error, setError] = useState<string>();
  const [history, setHistory] = useState(initialHistory);
  const [generations, setGenerations] = useState(initialGenerations);
  const [detailsLoading, setDetailsLoading] = useState(false);
  const [selected, setSelected] = useState<BackupGeneration>();
  const detailsController = useRef<AbortController | null>(null);

  const loadDetails = useCallback(
    async (signal?: AbortSignal) => {
      try {
        const [loadedPolicy, loadedHistory, loadedGenerations] =
          await Promise.all([
            fetchBackupPolicy(policy.resourceKind, policy.resourceId, signal),
            fetchBackupHistory(policy.resourceKind, policy.resourceId, signal),
            fetchBackupGenerations(
              policy.resourceKind,
              policy.resourceId,
              signal
            ),
          ]);
        setEnabled(loadedPolicy.enabled);
        setCron(loadedPolicy.cron ?? "0 3 * * *");
        setRetentionCount(String(loadedPolicy.retentionCount));
        onPolicyUpdated(loadedPolicy);
        setHistory(loadedHistory);
        setGenerations(loadedGenerations);
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
        if (!signal?.aborted) {
          setDetailsLoading(false);
        }
      }
    },
    [onPolicyUpdated, policy.resourceId, policy.resourceKind]
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

  useEffect(() => () => detailsController.current?.abort(), []);

  const refresh = async () => {
    detailsController.current?.abort();
    const controller = new AbortController();
    detailsController.current = controller;
    setDetailsLoading(true);
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
    if (!hasRunningBackup) {
      return;
    }
    const controller = new AbortController();
    let polling = false;
    const interval = window.setInterval(async () => {
      if (polling) {
        return;
      }
      polling = true;
      await loadDetails(controller.signal);
      polling = false;
    }, historyPollMilliseconds);
    return () => {
      controller.abort();
      window.clearInterval(interval);
    };
  }, [hasRunningBackup, loadDetails]);

  const retention = Math.trunc(Number(retentionCount));
  const policyValid =
    !busy &&
    Number.isInteger(retention) &&
    retention >= 1 &&
    retention <= 100 &&
    (!enabled || Boolean(cron.trim()));
  const visibleError = error || restore.error;

  return (
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
  );
};
