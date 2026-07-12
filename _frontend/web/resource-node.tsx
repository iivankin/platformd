import { Handle, Position } from "@xyflow/react";
import type { NodeProps } from "@xyflow/react";
import { Box, Database, HardDrive, Server } from "lucide-react";
import type { ComponentType } from "react";
import { memo } from "react";

import type { ResourceFlowNode } from "@/project-flow";

const resourceKinds: Record<
  ResourceFlowNode["data"]["kind"],
  { icon: ComponentType<{ className?: string }>; label: string }
> = {
  object_store: { icon: HardDrive, label: "Object storage" },
  postgres: { icon: Database, label: "PostgreSQL" },
  redis: { icon: Box, label: "Redis" },
  service: { icon: Server, label: "Service" },
};

const ResourceNodeComponent = ({
  data,
  selected,
}: NodeProps<ResourceFlowNode>) => {
  const kind = resourceKinds[data.kind];
  const Icon = kind.icon;
  const detail =
    data.imageReference ?? data.bucketName ?? data.internalHostname;

  return (
    <article
      className={
        selected
          ? "w-60 border border-foreground bg-background shadow-[0_2px_8px_oklch(0_0_0/10%)]"
          : "w-60 border border-border bg-background shadow-[0_1px_2px_oklch(0_0_0/6%)]"
      }
    >
      <Handle
        className="!size-2 !border-background !bg-muted-foreground"
        position={Position.Left}
        type="target"
      />
      <div className="flex h-9 items-center border-b border-border px-3">
        <Icon className="size-3.5 text-muted-foreground" />
        <span className="ml-2 min-w-0 flex-1 truncate text-xs font-medium">
          {data.name}
        </span>
        <span
          className={
            data.enabled
              ? "size-1.5 bg-emerald-500"
              : "size-1.5 bg-muted-foreground"
          }
          title={data.enabled ? "Enabled" : "Disabled"}
        />
      </div>
      <div className="px-3 py-2.5">
        <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          {kind.label}
        </p>
        <p className="mt-1.5 truncate text-[9px] text-muted-foreground">
          {detail}
        </p>
      </div>
      <Handle
        className="!size-2 !border-background !bg-muted-foreground"
        position={Position.Right}
        type="source"
      />
    </article>
  );
};

export const ResourceNode = memo(ResourceNodeComponent);
