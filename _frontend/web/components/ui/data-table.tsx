import type { CSSProperties, ReactNode } from "react";

import { cn } from "@/lib/utils";

interface DataTableProperties {
  children: ReactNode;
  className?: string;
  label: string;
}

export const DataTable = ({
  children,
  className,
  label,
}: DataTableProperties) => (
  <div className={cn("min-w-0 overflow-x-auto", className)}>
    <table aria-label={label} className="w-full min-w-max border-collapse">
      {children}
    </table>
  </div>
);

export const DataTableHeader = ({ children }: { children: ReactNode }) => (
  <thead className="sticky top-0 z-10 block bg-card text-[9px] tracking-[0.1em] text-muted-foreground uppercase">
    {children}
  </thead>
);

export const DataTableBody = ({ children }: { children: ReactNode }) => (
  <tbody className="block">{children}</tbody>
);

interface DataTableRowProperties {
  children: ReactNode;
  className?: string;
  columns: string;
  header?: boolean;
}

export const DataTableRow = ({
  children,
  className,
  columns,
  header = false,
}: DataTableRowProperties) => (
  <tr
    className={cn(
      "grid w-full min-w-max border-b border-border",
      header
        ? "bg-card"
        : "bg-background text-[10px] transition-colors duration-75 hover:bg-muted/35",
      className
    )}
    style={{ gridTemplateColumns: columns } as CSSProperties}
  >
    {children}
  </tr>
);

export const DataTableCell = ({
  children,
  className,
  header = false,
  title,
}: {
  children: ReactNode;
  className?: string;
  header?: boolean;
  title?: string;
}) =>
  header ? (
    <th
      className={cn("min-w-0 px-3 py-2.5 text-left font-medium", className)}
      scope="col"
      title={title}
    >
      {children}
    </th>
  ) : (
    <td
      className={cn("min-w-0 px-3 py-2.5 text-foreground", className)}
      title={title}
    >
      {children}
    </td>
  );
