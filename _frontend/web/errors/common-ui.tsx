import { LoaderCircle } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export const Eyebrow = ({ children }: { children: ReactNode }) => (
  <p className="text-[9px] font-medium tracking-[0.14em] text-muted-foreground uppercase">
    {children}
  </p>
);

export const StatusBadge = ({ value }: { value?: string }) => {
  const status = value?.toLowerCase() ?? "unknown";
  const critical = ["admin", "error", "fatal", "open"].includes(status);
  const positive = ["ok", "resolved"].includes(status);
  const warning = status === "warning";
  return (
    <span
      className={cn(
        "inline-flex border border-border px-1.5 py-0.5 text-[8px] tracking-[0.08em] text-muted-foreground uppercase",
        critical && "border-destructive/35 bg-destructive/5 text-destructive",
        positive && "border-emerald-500/35 bg-emerald-500/5 text-emerald-600",
        warning && "border-amber-500/35 bg-amber-500/5 text-amber-600"
      )}
    >
      {status}
    </span>
  );
};

export const EmptyView = ({ copy, title }: { copy: string; title: string }) => (
  <div className="grid min-h-72 place-items-center p-8 text-center">
    <div>
      <p className="text-sm font-medium">{title}</p>
      <p className="mt-2 max-w-md text-[10px] leading-5 text-muted-foreground">
        {copy}
      </p>
    </div>
  </div>
);

export const LoadingView = ({
  label = "Querying index",
}: {
  label?: string;
}) => (
  <div className="grid min-h-72 place-items-center p-8 text-[9px] tracking-[0.14em] text-muted-foreground uppercase">
    <div className="flex items-center gap-2">
      <LoaderCircle className="size-3.5 animate-spin" />
      {label}
    </div>
  </div>
);

export const ErrorView = ({ message }: { message: string }) => (
  <div className="grid min-h-72 place-items-center p-8 text-center text-[10px] text-destructive">
    {message}
  </div>
);

export const FormLabel = ({
  children,
  htmlFor,
}: {
  children: ReactNode;
  htmlFor: string;
}) => (
  <label
    className="mb-1.5 block text-[9px] tracking-[0.12em] text-muted-foreground uppercase"
    htmlFor={htmlFor}
  >
    {children}
  </label>
);
