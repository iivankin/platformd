import { Menu } from "@base-ui/react/menu";
import { MoreVertical, RotateCw, ScrollText, Trash2 } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";

import {
  fetchRuntimeDeployments,
  removeRuntimeDeployment,
  restartRuntimeDeployment,
} from "@/api";
import type { ManagedDeploymentKind, RuntimeDeployment } from "@/api";
import { Button } from "@/components/ui/button";
import { resourceDeploymentPath } from "@/project-resource-path";

const statusClass: Record<RuntimeDeployment["status"], string> = {
  failed: "bg-destructive",
  interrupted: "bg-amber-500",
  removed: "bg-muted-foreground",
  running: "bg-sky-500",
  succeeded: "bg-emerald-500",
};

const shortValue = (value: string, length = 24) =>
  value.length > length ? `${value.slice(0, length - 1)}…` : value;

const DeploymentMenu = ({
  active,
  busy,
  deployment,
  onRemove,
  onRestart,
  onViewLogs,
}: {
  active: boolean;
  busy: boolean;
  deployment: RuntimeDeployment;
  onRemove: () => void;
  onRestart: () => void;
  onViewLogs: () => void;
}) => (
  <Menu.Root>
    <Menu.Trigger
      aria-label={`Actions for deployment ${deployment.id}`}
      className="grid size-8 place-items-center border border-transparent text-muted-foreground hover:border-border hover:bg-muted hover:text-foreground"
      disabled={busy}
    >
      <MoreVertical className="size-3.5" />
    </Menu.Trigger>
    <Menu.Portal>
      <Menu.Positioner align="end" className="z-50" sideOffset={4}>
        <Menu.Popup className="min-w-44 border border-border bg-popover p-1 text-[10px] text-popover-foreground shadow-lg">
          <Menu.Item
            className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
            onClick={onViewLogs}
          >
            <ScrollText className="size-3.5" />
            View logs
          </Menu.Item>
          {active ? (
            <Menu.Item
              className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
              onClick={onRestart}
            >
              <RotateCw className="size-3.5" />
              Restart
            </Menu.Item>
          ) : null}
          <Menu.Item
            className="flex cursor-default items-center gap-2 px-2.5 py-2 text-destructive outline-none data-[highlighted]:bg-destructive/10"
            onClick={onRemove}
          >
            <Trash2 className="size-3.5" />
            Remove
          </Menu.Item>
        </Menu.Popup>
      </Menu.Positioner>
    </Menu.Portal>
  </Menu.Root>
);

const DeploymentRow = ({
  busy,
  deployment,
  onRemove,
  onRestart,
  onViewLogs,
  removeCandidate,
  setRemoveCandidate,
}: {
  busy: boolean;
  deployment: RuntimeDeployment;
  onRemove: () => void;
  onRestart: () => void;
  onViewLogs: () => void;
  removeCandidate?: string;
  setRemoveCandidate: (deploymentID?: string) => void;
}) => (
  <div className="border-b border-border last:border-b-0">
    <div className="grid min-h-16 grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-4 py-3">
      <div className="flex min-w-0 items-start gap-3">
        <span
          className={`mt-1.5 size-1.5 shrink-0 ${statusClass[deployment.status]}`}
        />
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[10px] font-medium capitalize">
              {deployment.status}
            </span>
            {deployment.active ? (
              <span className="border border-emerald-500/40 px-1.5 py-0.5 text-[8px] tracking-[0.1em] text-emerald-700 uppercase dark:text-emerald-300">
                Current
              </span>
            ) : null}
            <code className="text-[9px] text-muted-foreground">
              {shortValue(deployment.id, 18)}
            </code>
          </div>
          <p className="mt-1 truncate text-[9px] text-muted-foreground">
            {deployment.resourceKind}:{deployment.imageTag} ·{" "}
            {new Date(deployment.createdAt).toLocaleString()}
          </p>
          {deployment.errorMessage ? (
            <p className="mt-1 text-[9px] leading-4 text-destructive">
              {deployment.errorMessage}
            </p>
          ) : null}
        </div>
      </div>
      <DeploymentMenu
        active={deployment.active}
        busy={busy}
        deployment={deployment}
        onRemove={() => setRemoveCandidate(deployment.id)}
        onRestart={onRestart}
        onViewLogs={onViewLogs}
      />
    </div>
    {removeCandidate === deployment.id ? (
      <div className="border-t border-border bg-muted/15 px-4 py-3">
        <p className="text-[9px] leading-4 text-muted-foreground">
          {deployment.active
            ? "This stops the container and keeps its volume, data, deployment record, and logs. You can restart it later."
            : "This permanently removes this history record and its logs. The database volume is not touched."}
        </p>
        <div className="mt-2 flex justify-end gap-2">
          <Button
            onClick={() => setRemoveCandidate(undefined)}
            size="sm"
            variant="ghost"
          >
            Cancel
          </Button>
          <Button
            disabled={busy}
            onClick={onRemove}
            size="sm"
            variant="destructive"
          >
            {deployment.active ? "Stop deployment" : "Remove history"}
          </Button>
        </div>
      </div>
    ) : null}
  </div>
);

export const ManagedDeploymentHistory = ({
  kind,
  projectID,
  resourceID,
}: {
  kind: ManagedDeploymentKind;
  projectID: string;
  resourceID: string;
}) => {
  const navigate = useNavigate();
  const [deployments, setDeployments] = useState<RuntimeDeployment[]>([]);
  const [nextCursor, setNextCursor] = useState<string>();
  const [removeCandidate, setRemoveCandidate] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const load = useCallback(
    async (cursor?: string, signal?: AbortSignal) => {
      const page = await fetchRuntimeDeployments(
        projectID,
        kind,
        resourceID,
        cursor,
        signal
      );
      setDeployments((current) =>
        cursor ? [...current, ...page.deployments] : page.deployments
      );
      setNextCursor(page.nextCursor);
      setError(undefined);
    },
    [kind, projectID, resourceID]
  );

  useEffect(() => {
    const controller = new AbortController();
    const loadHistory = async () => {
      try {
        await load(undefined, controller.signal);
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
            : "Unable to load deployment history"
        );
      }
    };
    void loadHistory();
    return () => controller.abort();
  }, [load]);

  const runAction = async (action: () => Promise<void>) => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      await action();
      setRemoveCandidate(undefined);
      await load();
    } catch (actionError) {
      setError(
        actionError instanceof Error
          ? actionError.message
          : "Unable to update deployment"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <section>
      <header className="flex items-center justify-between border-b border-border px-4 py-3">
        <div>
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Deployments
          </h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Each deployment keeps its own bounded log history.
          </p>
        </div>
        <span className="text-[9px] text-muted-foreground">
          {deployments.length} loaded
        </span>
      </header>

      {error ? (
        <p className="border-b border-destructive/30 bg-destructive/5 px-4 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}

      {deployments.length ? (
        deployments.map((deployment) => (
          <DeploymentRow
            busy={busy}
            deployment={deployment}
            key={deployment.id}
            onRemove={() =>
              void runAction(() =>
                removeRuntimeDeployment(
                  projectID,
                  kind,
                  resourceID,
                  deployment.id
                )
              )
            }
            onRestart={() =>
              void runAction(() =>
                restartRuntimeDeployment(
                  projectID,
                  kind,
                  resourceID,
                  deployment.id
                )
              )
            }
            onViewLogs={() =>
              void navigate(
                resourceDeploymentPath(
                  projectID,
                  resourceID,
                  kind,
                  deployment.id
                )
              )
            }
            removeCandidate={removeCandidate}
            setRemoveCandidate={setRemoveCandidate}
          />
        ))
      ) : (
        <p className="border-b border-border px-4 py-12 text-center text-[10px] text-muted-foreground">
          No deployment attempts yet.
        </p>
      )}

      {nextCursor ? (
        <div className="flex justify-center border-b border-border px-4 py-4">
          <Button
            disabled={busy}
            onClick={() => void load(nextCursor)}
            size="sm"
            variant="ghost"
          >
            Load older
          </Button>
        </div>
      ) : null}
    </section>
  );
};
