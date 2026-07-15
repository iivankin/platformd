import type { ReactNode } from "react";

export const WorkspaceView = ({
  active,
  views,
}: {
  active: string;
  views: Record<string, ReactNode>;
}) => views[active] ?? null;
