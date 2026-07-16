import type { ReactNode } from "react";

export const WorkspaceView = ({
  active,
  views,
}: {
  active: string;
  views: Record<string, ReactNode>;
}) => (
  <div className="workspace-section-stack min-h-full">
    {views[active] ?? null}
  </div>
);
