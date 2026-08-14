import { Menu } from "@base-ui/react/menu";
import { MoreVertical } from "lucide-react";
import type { ComponentType } from "react";

import { cn } from "@/lib/utils";

export interface DeploymentAction {
  handleSelect: () => void;
  icon: ComponentType<{ className?: string }>;
  label: string;
  tone?: "default" | "destructive";
}

export const DeploymentActionMenu = ({
  actions,
  busy,
  deploymentID,
}: {
  actions: readonly DeploymentAction[];
  busy: boolean;
  deploymentID: string;
}) => (
  <Menu.Root>
    <Menu.Trigger
      aria-label={`Actions for deployment ${deploymentID}`}
      className="grid size-8 place-items-center border border-transparent text-muted-foreground hover:border-border hover:bg-muted hover:text-foreground"
      disabled={busy}
    >
      <MoreVertical className="size-3.5" />
    </Menu.Trigger>
    <Menu.Portal>
      <Menu.Positioner align="end" className="z-50" sideOffset={4}>
        <Menu.Popup className="min-w-48 border border-border bg-popover p-1 text-[10px] text-popover-foreground shadow-lg">
          {actions.map((action) => {
            const Icon = action.icon;
            return (
              <Menu.Item
                className={cn(
                  "flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted",
                  action.tone === "destructive" &&
                    "text-destructive data-[highlighted]:bg-destructive/10"
                )}
                key={action.label}
                onClick={action.handleSelect}
              >
                <Icon className="size-3.5" />
                {action.label}
              </Menu.Item>
            );
          })}
        </Menu.Popup>
      </Menu.Positioner>
    </Menu.Portal>
  </Menu.Root>
);
