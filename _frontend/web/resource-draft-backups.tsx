import { Check, Clock3, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router";

import { fetchBackupTargets } from "@/api";
import type { BackupTarget } from "@/api";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageStack } from "@/components/ui/page-stack";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import type { PendingBackupPolicy } from "@/pending-resource-creation";

const noBackupStorage = "__no-backup-storage__";

export const ResourceDraftBackups = ({
  onChange,
  policy,
}: {
  onChange: (policy: PendingBackupPolicy) => void;
  policy: PendingBackupPolicy;
}) => {
  const [targets, setTargets] = useState<BackupTarget[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const result = await fetchBackupTargets(controller.signal);
        setTargets(result.targets);
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load backup storage"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const invalidEnabledPolicy =
    policy.enabled && !(policy.targetId && policy.cron.trim());

  return (
    <PageStack>
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Backup schedule
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Configure encrypted backups before the resource is created.
          </p>
        </div>
        <div className="border-t border-border lg:border-t-0 lg:border-l">
          <button
            aria-pressed={policy.enabled}
            className="flex min-h-12 w-full items-center gap-3 border-b border-border px-5 text-left hover:bg-muted/40"
            onClick={() => onChange({ ...policy, enabled: !policy.enabled })}
            type="button"
          >
            <span
              className={cn(
                "grid size-6 place-items-center border",
                policy.enabled
                  ? "border-emerald-500/50 bg-emerald-500/10 text-emerald-600"
                  : "border-border text-muted-foreground"
              )}
            >
              {policy.enabled ? (
                <Check className="size-3" />
              ) : (
                <Clock3 className="size-3" />
              )}
            </span>
            <span className="text-[10px]">Automatic backups</span>
            <span className="ml-auto text-[9px] text-muted-foreground">
              {policy.enabled ? "On" : "Off"}
            </span>
          </button>

          <div className="grid md:grid-cols-[minmax(12rem,0.8fr)_minmax(14rem,1fr)_8rem]">
            <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
              <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Storage
              </span>
              {loading ? (
                <div className="mt-2 flex h-8 items-center gap-2 text-[9px] text-muted-foreground">
                  <LoaderCircle className="size-3 animate-spin" /> Loading
                </div>
              ) : (
                <Select
                  items={[
                    { label: "No storage", value: noBackupStorage },
                    ...targets.map((target) => ({
                      label: target.name,
                      value: target.id,
                    })),
                  ]}
                  onValueChange={(value) =>
                    onChange({
                      ...policy,
                      targetId: value === noBackupStorage ? "" : String(value),
                    })
                  }
                  value={policy.targetId || noBackupStorage}
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
              )}
            </div>
            <label
              className="border-b border-border px-5 py-4 md:border-r md:border-b-0"
              htmlFor="draft-backup-cron"
            >
              <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Schedule · UTC cron
              </span>
              <Input
                className="mt-2 font-mono"
                id="draft-backup-cron"
                onChange={(event) =>
                  onChange({ ...policy, cron: event.target.value })
                }
                placeholder="0 3 * * *"
                value={policy.cron}
              />
            </label>
            <label className="px-5 py-4" htmlFor="draft-backup-retention">
              <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Keep latest
              </span>
              <Input
                className="mt-2"
                id="draft-backup-retention"
                max={100}
                min={1}
                onChange={(event) =>
                  onChange({
                    ...policy,
                    retentionCount: Number(event.target.value),
                  })
                }
                type="number"
                value={policy.retentionCount}
              />
            </label>
          </div>
        </div>
      </SectionCard>

      <SectionCard className="px-5 py-4 text-[9px] leading-4 text-muted-foreground">
        {targets.length === 0 && !loading ? (
          <p>
            Connect a storage location in{" "}
            <Link className="underline" to="/backups/storage">
              Backups
            </Link>{" "}
            before enabling automatic backups.
          </p>
        ) : (
          <p>
            Backup history and manual backups become available after Deploy.
          </p>
        )}
        {invalidEnabledPolicy ? (
          <p className="mt-2 text-destructive">
            Automatic backups require storage and a cron schedule.
          </p>
        ) : null}
        {error ? <p className="mt-2 text-destructive">{error}</p> : null}
      </SectionCard>
    </PageStack>
  );
};
