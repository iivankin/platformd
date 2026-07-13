import { DatabaseBackup, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";
import type { ReactNode } from "react";

import { fetchBackupPolicies } from "@/api";
import type { BackupPolicy } from "@/api";

const resourceLabel: Record<BackupPolicy["resourceKind"], string> = {
  object_store: "Object Store",
  postgres: "PostgreSQL",
  redis: "Redis",
  registry: "Registry",
};

const nextRun = (policy: BackupPolicy) => {
  if (!policy.nextRunAt) {
    return "Disabled";
  }
  return new Date(policy.nextRunAt).toISOString().replace(".000Z", "Z");
};

const BackupResourceSummary = ({ policy }: { policy: BackupPolicy }) => (
  <div className="grid items-center gap-3 border-b border-border px-5 py-4 last:border-b-0 md:grid-cols-[minmax(12rem,1fr)_minmax(12rem,1fr)_minmax(12rem,1fr)_minmax(9rem,0.6fr)]">
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
    <div className="min-w-0">
      <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
        Next run
      </p>
      <p className="mt-1.5 truncate font-mono text-[9px]">{nextRun(policy)}</p>
    </div>
    <div>
      <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
        Retention
      </p>
      <p className="mt-1.5 text-[10px]">{policy.retentionCount} generations</p>
    </div>
  </div>
);

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
        populate this backup index.
      </p>
    );
  } else {
    content = ordered.map((policy) => (
      <BackupResourceSummary
        key={`${policy.resourceKind}:${policy.resourceId}`}
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
            Read-only index of every resource policy. Manage generations and
            restore inside the exact resource workspace.
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
