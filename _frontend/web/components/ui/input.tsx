import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

type InputProps = ComponentProps<"input">;

export const Input = ({ className, type = "text", ...props }: InputProps) => (
  <input
    className={cn(
      "h-8 w-full border border-input bg-background px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/55 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
      className
    )}
    data-slot="input"
    type={type}
    {...props}
  />
);
