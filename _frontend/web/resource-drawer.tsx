import type { ReactNode } from "react";
import { Link } from "react-router";

export const ResourceDrawer = ({
  children,
  closePath,
  label,
}: {
  children: ReactNode;
  closePath: string;
  label: string;
}) => (
  <div className="absolute inset-0 z-30 flex justify-end bg-background/55 backdrop-blur-[1px]">
    <Link
      aria-label={`Close ${label}`}
      className="absolute inset-0 cursor-default"
      to={closePath}
    />
    <aside
      aria-label={label}
      className="relative z-10 flex h-full w-full animate-in flex-col border-l border-border bg-background shadow-[-16px_0_40px_rgb(0_0_0/0.12)] duration-200 fade-in slide-in-from-right-2 md:w-[min(86vw,74rem)] md:min-w-[42rem] xl:w-[min(78vw,74rem)]"
    >
      {children}
    </aside>
  </div>
);
