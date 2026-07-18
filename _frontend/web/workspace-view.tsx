import type { ReactNode } from "react";

import { PageStack } from "@/components/ui/page-stack";

export const WorkspaceView = ({
  active,
  views,
}: {
  active: string;
  views: Record<string, ReactNode>;
}) => <PageStack>{views[active] ?? null}</PageStack>;
