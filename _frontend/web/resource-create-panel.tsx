import { Box, Database, HardDrive, Server, X } from "lucide-react";
import type { ComponentType } from "react";

import { Button } from "@/components/ui/button";

interface ResourceCreatePanelProperties {
  onClose: () => void;
  onSelect: (kind: "postgres" | "redis" | "service" | "storage") => void;
}

const options: {
  description: string;
  enabled: boolean;
  icon: ComponentType<{ className?: string }>;
  kind: "postgres" | "redis" | "service" | "storage";
  label: string;
}[] = [
  {
    description: "Run an OCI image with environment, health, and domains.",
    enabled: true,
    icon: Server,
    kind: "service",
    label: "Service",
  },
  {
    description: "RDB-only Redis with private DNS and managed credentials.",
    enabled: true,
    icon: Box,
    kind: "redis",
    label: "Redis",
  },
  {
    description: "Managed owner database and SQL workspace.",
    enabled: true,
    icon: Database,
    kind: "postgres",
    label: "PostgreSQL",
  },
  {
    description: "Encrypted private S3-compatible object storage.",
    enabled: true,
    icon: HardDrive,
    kind: "storage",
    label: "Object storage",
  },
];

export const ResourceCreatePanel = ({
  onClose,
  onSelect,
}: ResourceCreatePanelProperties) => (
  <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-[-8px_0_24px_oklch(0_0_0/5%)]">
    <div className="flex h-12 items-center border-b border-border px-4">
      <h2 className="text-xs font-medium">New resource</h2>
      <Button
        aria-label="Close resource picker"
        className="ml-auto"
        onClick={onClose}
        size="icon"
        variant="ghost"
      >
        <X />
      </Button>
    </div>
    <div className="px-4 py-5">
      <p className="mb-4 text-[10px] leading-4 text-muted-foreground">
        Resources join this project network and receive a stable internal DNS
        name.
      </p>
      <div className="border-t border-border">
        {options.map((option) => {
          const Icon = option.icon;
          const availability = option.enabled ? "" : "Next";
          return (
            <button
              className="group flex w-full items-start gap-3 border-b border-border px-1 py-4 text-left enabled:hover:bg-muted/40 disabled:cursor-not-allowed disabled:opacity-45"
              disabled={!option.enabled}
              key={option.kind}
              onClick={() => {
                if (
                  option.kind === "postgres" ||
                  option.kind === "redis" ||
                  option.kind === "service" ||
                  option.kind === "storage"
                ) {
                  onSelect(option.kind);
                }
              }}
              type="button"
            >
              <Icon className="mt-0.5 size-4 text-muted-foreground group-enabled:group-hover:text-foreground" />
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-2 text-xs font-medium">
                  {option.label}
                  {availability ? (
                    <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                      {availability}
                    </span>
                  ) : null}
                </span>
                <span className="mt-1 block text-[10px] leading-4 text-muted-foreground">
                  {option.description}
                </span>
              </span>
            </button>
          );
        })}
      </div>
    </div>
  </aside>
);
