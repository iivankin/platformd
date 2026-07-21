import { CheckCircle2, RefreshCw, TriangleAlert } from "lucide-react";

import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { useSelfUpdate } from "@/use-self-update";
import type { UpdateStatusState } from "@/use-update-status";

interface ReleasePresentation {
  action: string;
  description: string;
  title: string;
}

const releasePresentation = (
  update: UpdateStatusState,
  targetVersion: string | undefined
): ReleasePresentation => {
  if (targetVersion) {
    return {
      action: "Waiting for restart",
      description:
        "The verified release is installed. Waiting for the control plane to become ready.",
      title: `Restarting into ${targetVersion}`,
    };
  }
  if (update.status?.updateAvailable && update.status.updateSupported) {
    return {
      action: `Update to ${update.status.latestVersion}`,
      description: `Installed ${update.status.currentVersion}. The update is signed and supports a direct upgrade from this release.`,
      title: `${update.status.latestVersion} is available`,
    };
  }
  if (update.status?.updateAvailable) {
    return {
      action: "Manual upgrade required",
      description: `Installed ${update.status.currentVersion}. This release is not listed as a supported direct upgrade source.`,
      title: `${update.status.latestVersion} requires a manual upgrade`,
    };
  }
  if (update.status) {
    return {
      action: "Up to date",
      description: "The installed version matches the latest signed release.",
      title: `${update.status.currentVersion} is up to date`,
    };
  }
  if (update.error) {
    return {
      action: "Check first",
      description: update.error,
      title: "Update check unavailable",
    };
  }
  return {
    action: "Check first",
    description: "Verifying the latest signed platformd release manifest.",
    title: "Checking for updates…",
  };
};

const ReleaseIcon = ({
  checking,
  update,
  updating,
}: {
  checking: boolean;
  update: UpdateStatusState["status"];
  updating: boolean;
}) => {
  if (update && !update.updateAvailable) {
    return (
      <CheckCircle2 className="size-4 text-emerald-600 dark:text-emerald-400" />
    );
  }
  if (update?.updateAvailable && !update.updateSupported) {
    return (
      <TriangleAlert className="size-4 text-amber-600 dark:text-amber-400" />
    );
  }
  return (
    <RefreshCw
      className={cn(
        "size-4",
        (updating || checking) && "animate-spin",
        update?.updateAvailable && "text-cyan-600 dark:text-cyan-400"
      )}
    />
  );
};

export const PlatformReleaseOperation = ({
  update,
}: {
  update: UpdateStatusState;
}) => {
  const {
    error: updateError,
    start,
    targetVersion,
    updating,
  } = useSelfUpdate(update.refresh);
  const presentation = releasePresentation(update, targetVersion);
  const canApply = Boolean(
    update.status?.updateAvailable && update.status.updateSupported
  );

  return (
    <SectionCard className="flex flex-col gap-5 px-5 py-6 md:flex-row md:items-center">
      <div className="grid size-10 shrink-0 place-items-center bg-muted">
        <ReleaseIcon
          checking={update.checking}
          update={update.status}
          updating={updating}
        />
      </div>
      <div className="min-w-0 flex-1">
        <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
          Platform release
        </p>
        <p className="mt-1 text-sm font-medium">{presentation.title}</p>
        <p className="mt-2 max-w-2xl text-[10px] leading-4 text-muted-foreground">
          {presentation.description}
        </p>
        {update.checkedAt ? (
          <p className="mt-2 text-[9px] text-muted-foreground">
            Last checked {new Date(update.checkedAt).toLocaleString()}
          </p>
        ) : null}
        {update.error && update.status ? (
          <p className="mt-2 text-[10px] text-amber-600 dark:text-amber-300">
            Latest refresh failed: {update.error}
          </p>
        ) : null}
        {updateError ? (
          <p className="mt-2 text-[10px] text-rose-600 dark:text-rose-300">
            {updateError}
          </p>
        ) : null}
      </div>
      <div className="flex shrink-0 flex-wrap gap-2">
        <Button
          disabled={updating || update.checking}
          onClick={() => void update.refresh()}
          type="button"
          variant="outline"
        >
          <RefreshCw className={cn(update.checking && "animate-spin")} />
          {update.checking ? "Checking" : "Check again"}
        </Button>
        <Button
          disabled={updating || update.checking || !canApply}
          onClick={() => void start()}
          type="button"
        >
          {updating ? "Waiting for restart" : presentation.action}
        </Button>
      </div>
    </SectionCard>
  );
};
