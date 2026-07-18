import type * as React from "react";

import { cn } from "@/lib/utils";

export const PageStack = ({
  className,
  ...properties
}: React.ComponentProps<"div">) => (
  <div
    className={cn(
      "grid min-h-full content-start gap-3 bg-background p-4 sm:p-5",
      className
    )}
    data-slot="page-stack"
    {...properties}
  />
);
