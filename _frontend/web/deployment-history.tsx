import { RotateCcw } from "lucide-react";

import type { Deployment } from "@/api";
import { Button } from "@/components/ui/button";

interface DeploymentHistoryProperties {
  busy: boolean;
  deployments: Deployment[];
  nextCursor?: string;
  onCancelRollback: () => void;
  onLoadOlder: () => void;
  onRollback: (deployment: Deployment) => void;
  onSelectRollback: (deploymentID: string) => void;
  rollbackCandidate?: string;
}

const shortDigest = (digest: string) =>
  digest.length > 24 ? `${digest.slice(0, 20)}…` : digest;

const deploymentStatusClass = (status: Deployment["status"]) => {
  if (status === "succeeded") {
    return "mt-1 size-1.5 bg-emerald-500";
  }
  if (status === "running") {
    return "mt-1 size-1.5 bg-sky-500";
  }
  return "mt-1 size-1.5 bg-destructive";
};

export const DeploymentHistory = ({
  busy,
  deployments,
  nextCursor,
  onCancelRollback,
  onLoadOlder,
  onRollback,
  onSelectRollback,
  rollbackCandidate,
}: DeploymentHistoryProperties) => (
  <section>
    <div className="flex items-center justify-between border-b border-border px-4 py-3">
      <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
        Deployments
      </h3>
      <span className="text-[9px] text-muted-foreground">
        {deployments.length}
      </span>
    </div>
    {deployments.length === 0 ? (
      <p className="px-4 py-8 text-center text-[10px] text-muted-foreground">
        No deployment attempts yet
      </p>
    ) : (
      <div>
        {deployments.map((deployment) => (
          <div className="border-b border-border px-4 py-3" key={deployment.id}>
            <div className="flex items-start gap-3">
              <span className={deploymentStatusClass(deployment.status)} />
              <div className="min-w-0 flex-1">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-[10px] font-medium capitalize">
                    {deployment.status}
                  </span>
                  <span className="text-[9px] text-muted-foreground">
                    {new Date(deployment.createdAt).toLocaleString()}
                  </span>
                </div>
                <p className="mt-1 truncate text-[9px] text-muted-foreground">
                  {shortDigest(deployment.imageDigest)}
                </p>
                {deployment.errorMessage ? (
                  <p className="mt-1 text-[9px] leading-4 text-destructive">
                    {deployment.errorMessage}
                  </p>
                ) : null}
              </div>
              {deployment.status === "succeeded" ? (
                <Button
                  aria-label={`Rollback to deployment ${deployment.id}`}
                  disabled={busy}
                  onClick={() => onSelectRollback(deployment.id)}
                  size="icon"
                  variant="ghost"
                >
                  <RotateCcw />
                </Button>
              ) : null}
            </div>
            {rollbackCandidate === deployment.id ? (
              <div className="mt-3 border-t border-border pt-3">
                <p className="text-[9px] leading-4 text-muted-foreground">
                  This copies the immutable configuration and pins its exact
                  digest. Writable volume data is not rolled back.
                </p>
                <div className="mt-2 flex justify-end gap-2">
                  <Button onClick={onCancelRollback} size="sm" variant="ghost">
                    Cancel
                  </Button>
                  <Button
                    disabled={busy}
                    onClick={() => onRollback(deployment)}
                    size="sm"
                    variant="destructive"
                  >
                    Apply rollback
                  </Button>
                </div>
              </div>
            ) : null}
          </div>
        ))}
      </div>
    )}
    {nextCursor ? (
      <div className="flex justify-center px-4 py-4">
        <Button disabled={busy} onClick={onLoadOlder} size="sm" variant="ghost">
          Load older
        </Button>
      </div>
    ) : null}
  </section>
);
