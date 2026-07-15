import { LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import {
  fetchBackupGenerations,
  fetchBackupHistory,
  fetchBackupPolicy,
} from "@/api";
import type {
  BackupGeneration,
  BackupPolicy,
  BackupRecord,
  RecoveryResourceKind,
} from "@/api";
import { BackupResourceRow } from "@/backup-resource-row";

interface BackupWorkspaceData {
  generations: BackupGeneration[];
  history: BackupRecord[];
  policy: BackupPolicy;
}

export const ResourceBackupPanel = ({
  resourceID,
  resourceKind,
}: {
  resourceID: string;
  resourceKind: RecoveryResourceKind;
}) => {
  const [data, setData] = useState<BackupWorkspaceData>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [policy, history, generations] = await Promise.all([
          fetchBackupPolicy(resourceKind, resourceID, controller.signal),
          fetchBackupHistory(resourceKind, resourceID, controller.signal),
          fetchBackupGenerations(resourceKind, resourceID, controller.signal),
        ]);
        setData({ generations, history, policy });
        setError(undefined);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load backup policy"
        );
      }
    };
    void load();
    return () => controller.abort();
  }, [resourceID, resourceKind]);

  if (error) {
    return (
      <p className="border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
        {error}
      </p>
    );
  }
  if (!data) {
    return (
      <div className="flex items-center gap-2 border-b border-border px-5 py-3 text-[10px] text-muted-foreground">
        <LoaderCircle className="size-3 animate-spin" />
        Loading backup policy
      </div>
    );
  }
  return (
    <BackupResourceRow
      initialGenerations={data.generations}
      initialHistory={data.history}
      onPolicyUpdated={(policy) =>
        setData((current) => current && { ...current, policy })
      }
      policy={data.policy}
    />
  );
};
