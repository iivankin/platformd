import { DatabaseBackup, LoaderCircle } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";

import { fetchBackupPolicies } from "@/api";
import type { BackupPolicy } from "@/api";
import { BackupResourceRow } from "@/backup-resource-row";

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : "Unable to load backup resources";

export const BackupResources = () => {
  const [policies, setPolicies] = useState<BackupPolicy[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setPolicies(await fetchBackupPolicies(controller.signal));
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError));
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    const initial = window.setTimeout(() => void load(), 0);
    return () => {
      controller.abort();
      window.clearTimeout(initial);
    };
  }, []);

  const updatePolicy = useCallback((updated: BackupPolicy) => {
    setPolicies((current) =>
      current.map((policy) =>
        policy.resourceKind === updated.resourceKind &&
        policy.resourceId === updated.resourceId
          ? updated
          : policy
      )
    );
  }, []);

  const ordered = policies.toSorted((left, right) => {
    const kind = left.resourceKind.localeCompare(right.resourceKind);
    return kind || left.resourceId.localeCompare(right.resourceId);
  });
  const enabled = policies.filter((policy) => policy.enabled).length;
  let content: ReactNode;
  if (loading) {
    content = (
      <div className="flex items-center gap-2 px-5 py-5 text-[10px] text-muted-foreground">
        <LoaderCircle className="size-3.5 animate-spin" />
        Loading resource policies
      </div>
    );
  } else if (ordered.length === 0) {
    content = (
      <p className="px-5 py-5 text-[10px] text-muted-foreground">
        Create PostgreSQL, Redis, Registry, or Object Store resources to
        configure their backups.
      </p>
    );
  } else {
    content = ordered.map((policy) => (
      <BackupResourceRow
        key={`${policy.resourceKind}:${policy.resourceId}`}
        onPolicyUpdated={updatePolicy}
        policy={policy}
      />
    ));
  }

  return (
    <section className="border-b border-border">
      <div className="flex items-center gap-3 border-b border-border px-5 py-4">
        <DatabaseBackup className="size-4 text-muted-foreground" />
        <div>
          <h2 className="text-[10px] font-medium">Resource backups</h2>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Independent UTC schedule, retention, generations, and restore for
            every exact managed resource.
          </p>
        </div>
        <span className="ml-auto font-mono text-[9px] text-muted-foreground">
          {enabled}/{policies.length} scheduled
        </span>
      </div>

      {content}

      {error ? (
        <p className="border-t border-rose-500/30 bg-rose-500/5 px-5 py-3 text-[10px] text-rose-600 dark:text-rose-300">
          {error}
        </p>
      ) : null}
    </section>
  );
};
