import type { ReactNode } from "react";

export const FormField = ({
  children,
  label,
  name,
}: {
  children: ReactNode;
  label: string;
  name: string;
}) => (
  <div className="mb-4">
    <label
      className="mb-1.5 block text-[9px] tracking-[0.12em] text-muted-foreground uppercase"
      htmlFor={name}
    >
      {label}
    </label>
    {children}
  </div>
);
