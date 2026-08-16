import type { ReactNode } from "react";

import { PageStack } from "@/components/ui/page-stack";
import { cn } from "@/lib/utils";

export const WorkspaceView = ({
  active,
  internallyScrollable = active === "telemetry",
  views,
}: {
  active: string;
  internallyScrollable?: boolean;
  views: Record<string, ReactNode>;
}) => (
  <PageStack
    className={cn(internallyScrollable && "h-full min-h-0 overflow-hidden")}
  >
    {views[active] ?? null}
  </PageStack>
);
