import { Copy, PlugZap } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";

export interface ConnectionDetail {
  label: string;
  value: string;
}

export const ConnectionDetails = ({
  description,
  rows,
  title = "Connection details",
}: {
  description?: string;
  rows: ConnectionDetail[];
  title?: string;
}) => {
  const [copied, setCopied] = useState<string>();

  const copy = (row: ConnectionDetail) => {
    void navigator.clipboard.writeText(row.value);
    setCopied(row.label);
  };

  return (
    <section className="border-b border-border">
      <header className="flex items-center gap-3 border-b border-border px-4 py-3">
        <PlugZap className="size-3.5 text-muted-foreground" />
        <div>
          <h3 className="text-[10px] font-medium">{title}</h3>
          {description ? (
            <p className="mt-0.5 text-[9px] text-muted-foreground">
              {description}
            </p>
          ) : null}
        </div>
      </header>
      <dl>
        {rows.map((row) => (
          <div
            className="grid min-h-11 grid-cols-[8rem_minmax(0,1fr)_2rem] items-center border-b border-border last:border-b-0"
            key={row.label}
          >
            <dt className="px-4 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              {row.label}
            </dt>
            <dd className="min-w-0 border-x border-border px-3 py-2">
              <code className="block overflow-x-auto text-[9px] leading-4 whitespace-nowrap select-all">
                {row.value}
              </code>
            </dd>
            <dd className="grid place-items-center">
              <Button
                aria-label={`Copy ${row.label}`}
                onClick={() => copy(row)}
                size="icon"
                title={copied === row.label ? "Copied" : `Copy ${row.label}`}
                variant="ghost"
              >
                <Copy />
              </Button>
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
};
