import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { Box, Database, HardDrive, Network, Server } from "lucide-react";
import type { ComponentType } from "react";
import { memo } from "react";

import type { ResourceFlowNode } from "@/project-flow";

const resourceKinds: Record<
  ResourceFlowNode["data"]["kind"],
  { icon: ComponentType<{ className?: string }>; label: string }
> = {
  network_gateway: { icon: Network, label: "Network gateway" },
  object_store: { icon: HardDrive, label: "Object storage" },
  postgres: { icon: Database, label: "PostgreSQL" },
  redis: { icon: Box, label: "Redis" },
  service: { icon: Server, label: "Service" },
};

const statusStyles: Record<ResourceFlowNode["data"]["status"], string> = {
  degraded: "bg-amber-500",
  disabled: "bg-muted-foreground",
  failed: "bg-destructive",
  pending: "bg-sky-500",
  running: "bg-emerald-500",
};

const nodeClassName = (selected: boolean, pending: boolean) => {
  if (pending) {
    return "flex w-64 flex-col border border-sky-500 bg-background shadow-[0_2px_8px_oklch(0_0_0/10%)]";
  }
  if (selected) {
    return "flex w-64 flex-col border border-foreground bg-background shadow-[0_2px_8px_oklch(0_0_0/10%)]";
  }
  return "flex w-64 flex-col border border-border bg-background shadow-[0_1px_2px_oklch(0_0_0/6%)]";
};

const gatewayDetail = (data: ResourceFlowNode["data"]) => {
  if (data.kind !== "network_gateway") {
    return;
  }
  if (data.gatewayMode === "import") {
    return `${data.gatewayRemoteHost}:${data.gatewayRemotePort}`;
  }
  return `${data.gatewayProtocol?.toUpperCase()} ${data.gatewaySourceAddress}:${data.gatewayListenPort}`;
};

const connectionHandleTop = (index: number, count: number): string =>
  `${((index + 1) / (count + 1)) * 100}%`;

const ResourceNodeComponent = ({
  data,
  selected,
}: NodeProps<ResourceFlowNode>) => {
  const kind = resourceKinds[data.kind];
  const Icon = kind.icon;
  const incomingHandleIDs = data.incomingHandleIDs ?? [];
  const outgoingHandleIDs = data.outgoingHandleIDs ?? [];
  const detail =
    gatewayDetail(data) ??
    (data.source?.type === "github"
      ? data.source.github.repository
      : data.source?.image.reference) ??
    data.imageReference ??
    data.bucketName ??
    data.internalHostname;

  return (
    <article
      className={nodeClassName(selected, Boolean(data.pendingChangeCount))}
      style={{ height: data.layoutHeight }}
    >
      {incomingHandleIDs.map((handleID, index) => (
        <Handle
          className="!size-2.5 !border-background !bg-muted-foreground"
          id={handleID}
          isConnectable={false}
          key={handleID}
          position={Position.Left}
          style={{ top: connectionHandleTop(index, incomingHandleIDs.length) }}
          type="target"
        />
      ))}
      <div className="flex h-9 items-center border-b border-border px-3">
        <Icon className="size-3.5 text-muted-foreground" />
        <span className="ml-2 min-w-0 flex-1 truncate text-xs font-medium">
          {data.name}
        </span>
        {data.pendingChangeCount ? (
          <span className="mr-2 bg-sky-500/15 px-1.5 py-0.5 text-[8px] font-medium text-sky-600 uppercase dark:text-sky-300">
            {data.draft ? "Draft" : "Edited"}
          </span>
        ) : null}
        <span
          className={`size-1.5 ${statusStyles[data.status]}`}
          title={data.statusMessage || data.status}
        />
      </div>
      <div className="min-h-0 flex-1 px-3 py-2.5">
        <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          {kind.label}
        </p>
        <p className="mt-1.5 truncate text-[9px] text-muted-foreground">
          {detail}
        </p>
        <p className="mt-1 truncate text-[9px] text-muted-foreground capitalize">
          {data.status}
        </p>
      </div>
      {data.pendingChangeCount ? (
        <div className="flex h-[34px] shrink-0 items-center gap-2 border-t border-sky-500/30 bg-sky-500/5 px-3 text-[9px] text-sky-600 dark:text-sky-300">
          <span className="grid size-3.5 place-items-center border border-current">
            !
          </span>
          {data.draft ? (
            "Pending creation"
          ) : (
            <>
              {data.pendingChangeCount}{" "}
              {data.pendingChangeCount === 1 ? "change" : "changes"}
            </>
          )}
        </div>
      ) : null}
      {data.volumes.length ? (
        <div className="border-t border-border bg-muted/20">
          {data.volumes.map((volume) => (
            <div
              className="flex h-[33px] items-center gap-2 border-b border-border px-3 last:border-b-0"
              key={volume.id}
            >
              <HardDrive className="size-3 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate text-[9px] text-muted-foreground">
                {volume.name}
              </span>
              <span
                className="max-w-24 truncate font-mono text-[8px] text-muted-foreground/70"
                title={volume.containerPath ?? "Not mounted"}
              >
                {volume.containerPath ?? "detached"}
              </span>
            </div>
          ))}
        </div>
      ) : null}
      {outgoingHandleIDs.map((handleID, index) => (
        <Handle
          className="!size-2.5 !border-background !bg-muted-foreground"
          id={handleID}
          isConnectable={false}
          key={handleID}
          position={Position.Right}
          style={{ top: connectionHandleTop(index, outgoingHandleIDs.length) }}
          type="source"
        />
      ))}
    </article>
  );
};

export const ResourceNode = memo(ResourceNodeComponent);
