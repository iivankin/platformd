import { Box, Database, HardDrive, Server, X } from "lucide-react";
import type { ComponentType } from "react";

import { Button } from "@/components/ui/button";
import type { ResourceNodeData } from "@/project-flow";

const resourceKinds: Record<
  ResourceNodeData["kind"],
  { icon: ComponentType<{ className?: string }>; label: string }
> = {
  object_store: { icon: HardDrive, label: "Object storage" },
  postgres: { icon: Database, label: "PostgreSQL" },
  redis: { icon: Box, label: "Redis" },
  service: { icon: Server, label: "Service" },
};

const DetailRow = ({ label, value }: { label: string; value?: string }) => {
  if (!value) {
    return null;
  }
  return (
    <div className="grid grid-cols-[7rem_minmax(0,1fr)] gap-3 border-b border-border py-3 last:border-b-0">
      <dt className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
        {label}
      </dt>
      <dd className="min-w-0 text-[10px] leading-4 break-all">{value}</dd>
    </div>
  );
};

const statusColor = (status: ResourceNodeData["status"]) => {
  switch (status) {
    case "running": {
      return "bg-emerald-500";
    }
    case "degraded": {
      return "bg-amber-500";
    }
    case "failed": {
      return "bg-destructive";
    }
    case "pending": {
      return "bg-sky-500";
    }
    default: {
      return "bg-muted-foreground";
    }
  }
};

export const ResourceDetailPanel = ({
  data,
  onClose,
}: {
  data: ResourceNodeData;
  onClose: () => void;
}) => {
  const kind = resourceKinds[data.kind];
  const Icon = kind.icon;
  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-[-8px_0_24px_oklch(0_0_0/5%)]">
      <div className="flex h-12 items-center border-b border-border px-4">
        <Icon className="size-4 text-muted-foreground" />
        <div className="ml-2 min-w-0">
          <h2 className="truncate text-xs font-medium">{data.name}</h2>
          <p className="text-[9px] text-muted-foreground">{kind.label}</p>
        </div>
        <Button
          aria-label="Close resource details"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>

      <div className="border-b border-border px-4 py-4">
        <div className="flex items-center gap-2">
          <span className={`size-1.5 ${statusColor(data.status)}`} />
          <span className="text-[10px] font-medium capitalize">
            {data.status}
          </span>
        </div>
        {data.statusMessage ? (
          <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
            {data.statusMessage}
          </p>
        ) : null}
      </div>

      <dl className="px-4">
        <DetailRow label="Internal DNS" value={data.internalHostname} />
        <DetailRow label="Image" value={data.imageReference} />
        <DetailRow label="Digest" value={data.imageDigest} />
        <DetailRow label="Deployment" value={data.activeDeploymentId} />
        <DetailRow label="Bucket" value={data.bucketName} />
      </dl>
    </aside>
  );
};
