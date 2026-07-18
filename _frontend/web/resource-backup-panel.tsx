import { LoaderCircle } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";

import {
  fetchBackupGenerations,
  fetchBackupHistory,
  fetchBackupPolicy,
  fetchBackupTargets,
} from "@/api";
import type {
  BackupGeneration,
  BackupPolicy,
  BackupRecord,
  BackupTarget,
  RecoveryResourceKind,
} from "@/api";
import { BackupResourceRow } from "@/backup-resource-row";
import { SectionCard } from "@/components/ui/card";

interface BackupWorkspaceData {
  generations: BackupGeneration[];
  history: BackupRecord[];
  policy: BackupPolicy;
  targetID: string;
  targets: BackupTarget[];
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
  const handlePolicyUpdated = useCallback((policy: BackupPolicy) => {
    setData((current) => current && { ...current, policy });
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [policy, storage] = await Promise.all([
          fetchBackupPolicy(resourceKind, resourceID, controller.signal),
          fetchBackupTargets(controller.signal),
        ]);
        const targetID = policy.targetId || storage.targets[0]?.id || "";
        const [history, generations] = targetID
          ? await Promise.all([
              fetchBackupHistory(
                resourceKind,
                resourceID,
                targetID,
                controller.signal
              ),
              fetchBackupGenerations(
                resourceKind,
                resourceID,
                targetID,
                controller.signal
              ),
            ])
          : [[], []];
        setData({
          generations,
          history,
          policy,
          targetID,
          targets: storage.targets,
        });
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
      <SectionCard className="bg-destructive/5 px-5 py-3 text-[10px] text-destructive ring-destructive/30">
        {error}
      </SectionCard>
    );
  }
  if (!data) {
    return (
      <SectionCard className="flex items-center gap-2 px-5 py-3 text-[10px] text-muted-foreground">
        <LoaderCircle className="size-3 animate-spin" />
        Loading backups
      </SectionCard>
    );
  }
  if (data.targets.length === 0) {
    return (
      <SectionCard className="px-5 py-4 text-[10px] text-muted-foreground">
        Connect a storage location in{" "}
        <Link className="underline" to="/backups/storage">
          Backups
        </Link>{" "}
        before creating backups.
      </SectionCard>
    );
  }
  return (
    <BackupResourceRow
      initialGenerations={data.generations}
      initialHistory={data.history}
      initialTargetID={data.targetID}
      onPolicyUpdated={handlePolicyUpdated}
      policy={data.policy}
      targets={data.targets}
    />
  );
};
