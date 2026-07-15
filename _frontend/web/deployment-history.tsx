import { Menu } from "@base-ui/react/menu";
import {
  MoreVertical,
  Play,
  RefreshCw,
  RotateCw,
  ScrollText,
  Trash2,
} from "lucide-react";
import { useState } from "react";

import type { Deployment } from "@/api";
import { Button } from "@/components/ui/button";

interface DeploymentHistoryProperties {
  activeDeploymentID?: string;
  busy: boolean;
  deployments: Deployment[];
  nextCursor?: string;
  onDeployVersion: (deployment: Deployment) => void;
  onLoadOlder: () => void;
  onRedeploy: (deployment: Deployment) => void;
  onRemove: (deployment: Deployment) => void;
  onRestart: (deployment: Deployment) => void;
  onViewLogs: (deployment: Deployment) => void;
}

interface Confirmation {
  action: "deploy" | "remove";
  deploymentID: string;
}

const shortValue = (value: string, length = 24) =>
  value.length > length ? `${value.slice(0, length - 1)}…` : value;

const deploymentStatusClass = (status: Deployment["status"]) => {
  if (status === "succeeded") {
    return "bg-emerald-500";
  }
  if (status === "running") {
    return "bg-sky-500";
  }
  if (status === "interrupted") {
    return "bg-amber-500";
  }
  return "bg-destructive";
};

const confirmationMessage = (confirmation: Confirmation, active: boolean) => {
  if (confirmation.action === "deploy") {
    return "A new deployment will be created from this exact image digest and configuration snapshot. Volume contents stay current.";
  }
  if (active) {
    return "This stops the active deployment. Service volumes and deployment logs are preserved.";
  }
  return "This permanently removes this history record and its deployment logs.";
};

const confirmationLabel = (
  confirmation: Confirmation,
  deployment: Deployment,
  active: boolean
) => {
  if (confirmation.action === "deploy") {
    return deployment.status === "failed"
      ? "Retry deployment"
      : "Deploy version";
  }
  return active ? "Stop deployment" : "Remove history";
};

const DeploymentActions = ({
  active,
  busy,
  deployment,
  onConfirm,
  onRedeploy,
  onRestart,
  onViewLogs,
}: {
  active: boolean;
  busy: boolean;
  deployment: Deployment;
  onConfirm: (confirmation: Confirmation) => void;
  onRedeploy: () => void;
  onRestart: () => void;
  onViewLogs: () => void;
}) => {
  const deployable =
    !active &&
    (deployment.status === "succeeded" || deployment.status === "failed");
  const deployActionLabel =
    deployment.status === "failed" ? "Retry" : "Deploy this version";

  return (
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
          <Menu.Popup className="min-w-48 border border-border bg-popover p-1 text-[10px] text-popover-foreground shadow-lg">
            <Menu.Item
              className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
              onClick={onViewLogs}
            >
              <ScrollText className="size-3.5" />
              View logs
            </Menu.Item>
            {active && (
              <>
                <Menu.Item
                  className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
                  onClick={onRestart}
                >
                  <RotateCw className="size-3.5" />
                  Restart
                </Menu.Item>
                <Menu.Item
                  className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
                  onClick={onRedeploy}
                >
                  <RefreshCw className="size-3.5" />
                  Redeploy
                </Menu.Item>
              </>
            )}
            {deployable && (
              <Menu.Item
                className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
                onClick={() =>
                  onConfirm({ action: "deploy", deploymentID: deployment.id })
                }
              >
                <Play className="size-3.5" />
                {deployActionLabel}
              </Menu.Item>
            )}
            <Menu.Item
              className="flex cursor-default items-center gap-2 px-2.5 py-2 text-destructive outline-none data-[highlighted]:bg-destructive/10"
              onClick={() =>
                onConfirm({ action: "remove", deploymentID: deployment.id })
              }
            >
              <Trash2 className="size-3.5" />
              Remove
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
};

const DeploymentRow = ({
  active,
  busy,
  confirmation,
  deployment,
  onCancel,
  onConfirm,
  onDeployVersion,
  onRedeploy,
  onRemove,
  onRestart,
  onViewLogs,
}: {
  active: boolean;
  busy: boolean;
  confirmation?: Confirmation;
  deployment: Deployment;
  onCancel: () => void;
  onConfirm: (confirmation: Confirmation) => void;
  onDeployVersion: () => void;
  onRedeploy: () => void;
  onRemove: () => void;
  onRestart: () => void;
  onViewLogs: () => void;
}) => (
  <div className="border-b border-border last:border-b-0">
    <div className="grid min-h-16 grid-cols-[minmax(0,1fr)_auto] items-center gap-4 px-4 py-3">
      <div className="flex min-w-0 items-start gap-3">
        <span
          className={`mt-1.5 size-1.5 shrink-0 ${deploymentStatusClass(deployment.status)}`}
        />
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className="text-[10px] font-medium capitalize">
              {deployment.status}
            </span>
            {active ? (
              <span className="border border-emerald-500/40 px-1.5 py-0.5 text-[8px] tracking-[0.1em] text-emerald-700 uppercase dark:text-emerald-300">
                Active
              </span>
            ) : null}
            <code className="text-[9px] text-muted-foreground">
              {shortValue(deployment.id, 18)}
            </code>
          </div>
          <p className="mt-1 truncate text-[9px] text-muted-foreground">
            {shortValue(deployment.imageDigest)} ·{" "}
            {new Date(deployment.createdAt).toLocaleString()}
          </p>
          {deployment.errorMessage ? (
            <p className="mt-1 text-[9px] leading-4 text-destructive">
              {deployment.errorMessage}
            </p>
          ) : null}
        </div>
      </div>
      <DeploymentActions
        active={active}
        busy={busy}
        deployment={deployment}
        onConfirm={onConfirm}
        onRedeploy={onRedeploy}
        onRestart={onRestart}
        onViewLogs={onViewLogs}
      />
    </div>

    {confirmation?.deploymentID === deployment.id ? (
      <div className="border-t border-border bg-muted/15 px-4 py-3">
        <p className="text-[9px] leading-4 text-muted-foreground">
          {confirmationMessage(confirmation, active)}
        </p>
        <div className="mt-2 flex justify-end gap-2">
          <Button onClick={onCancel} size="sm" variant="ghost">
            Cancel
          </Button>
          <Button
            disabled={busy}
            onClick={
              confirmation.action === "deploy" ? onDeployVersion : onRemove
            }
            size="sm"
            variant={
              confirmation.action === "remove" ? "destructive" : "default"
            }
          >
            {confirmationLabel(confirmation, deployment, active)}
          </Button>
        </div>
      </div>
    ) : null}
  </div>
);

export const DeploymentHistory = ({
  activeDeploymentID,
  busy,
  deployments,
  nextCursor,
  onDeployVersion,
  onLoadOlder,
  onRedeploy,
  onRemove,
  onRestart,
  onViewLogs,
}: DeploymentHistoryProperties) => {
  const [confirmation, setConfirmation] = useState<Confirmation>();
  const primary =
    deployments.find((deployment) => deployment.id === activeDeploymentID) ??
    deployments[0];
  const history = primary
    ? deployments.filter((deployment) => deployment.id !== primary.id)
    : [];

  if (!primary) {
    return (
      <section className="grid min-h-64 place-items-center border-b border-border px-4 text-center">
        <div>
          <p className="text-[10px] font-medium">No deployment attempts yet</p>
          <p className="mt-2 text-[9px] text-muted-foreground">
            The first deployment will appear here with its logs.
          </p>
        </div>
      </section>
    );
  }

  const row = (deployment: Deployment, active: boolean) => (
    <DeploymentRow
      active={active}
      busy={busy}
      confirmation={confirmation}
      deployment={deployment}
      key={deployment.id}
      onCancel={() => setConfirmation(undefined)}
      onConfirm={setConfirmation}
      onDeployVersion={() => {
        setConfirmation(undefined);
        onDeployVersion(deployment);
      }}
      onRedeploy={() => onRedeploy(deployment)}
      onRemove={() => {
        setConfirmation(undefined);
        onRemove(deployment);
      }}
      onRestart={() => onRestart(deployment)}
      onViewLogs={() => onViewLogs(deployment)}
    />
  );

  return (
    <section>
      <header className="flex items-center justify-between border-b border-border px-4 py-3">
        <div>
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            {primary.id === activeDeploymentID
              ? "Active deployment"
              : "Latest attempt"}
          </h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Open logs and actions for the exact deployment.
          </p>
        </div>
        <span className="text-[9px] text-muted-foreground">
          {deployments.length} total
        </span>
      </header>

      {row(primary, primary.id === activeDeploymentID)}

      <div className="flex items-center justify-between border-y border-border bg-muted/10 px-4 py-2.5">
        <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
          History
        </h3>
        <span className="text-[9px] text-muted-foreground">
          {history.length}
        </span>
      </div>

      {history.length ? (
        history.map((deployment) => row(deployment, false))
      ) : (
        <p className="border-b border-border px-4 py-8 text-center text-[10px] text-muted-foreground">
          No previous deployments
        </p>
      )}

      {nextCursor ? (
        <div className="flex justify-center border-b border-border px-4 py-4">
          <Button
            disabled={busy}
            onClick={onLoadOlder}
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
