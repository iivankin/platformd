import { LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchBackupPolicy } from "@/api";
import type { BackupPolicy, RecoveryResourceKind } from "@/api";
import { BackupResourceRow } from "@/backup-resource-row";

export const ResourceBackupPanel = ({
  resourceID,
  resourceKind,
}: {
  resourceID: string;
  resourceKind: RecoveryResourceKind;
}) => {
  const [policy, setPolicy] = useState<BackupPolicy>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setPolicy(
          await fetchBackupPolicy(resourceKind, resourceID, controller.signal)
        );
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
  if (!policy) {
    return (
      <div className="flex items-center gap-2 border-b border-border px-5 py-3 text-[10px] text-muted-foreground">
        <LoaderCircle className="size-3 animate-spin" />
        Loading backup policy
      </div>
    );
  }
  return (
    <div className="border-b border-border">
      <BackupResourceRow onPolicyUpdated={setPolicy} policy={policy} />
    </div>
  );
};
