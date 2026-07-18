import type * as React from "react";

import { cn } from "@/lib/utils";

const cardSurfaceClassName =
  "overflow-hidden rounded-none bg-card text-xs/relaxed text-card-foreground ring-1 ring-foreground/10";

export const SectionCard = ({
  className,
  ...properties
}: React.ComponentProps<"section">) => (
  <section
    className={cn(cardSurfaceClassName, className)}
    data-size="default"
    data-slot="card"
    {...properties}
  />
);

export const FormCard = ({
  className,
  ...properties
}: React.ComponentProps<"form">) => (
  <form
    className={cn(cardSurfaceClassName, className)}
    data-size="default"
    data-slot="card"
    {...properties}
  />
);
