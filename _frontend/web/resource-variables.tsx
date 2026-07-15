import { Check, Copy } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";

export interface ResourceVariable {
  name: string;
  value: string;
}

export const ResourceVariables = ({
  description,
  variables,
}: {
  description: string;
  variables: ResourceVariable[];
}) => {
  const [copied, setCopied] = useState<string>();

  const copy = async (variable: ResourceVariable) => {
    await navigator.clipboard.writeText(variable.value);
    setCopied(variable.name);
    window.setTimeout(() => setCopied(undefined), 1200);
  };

  return (
    <section>
      <header className="border-b border-border px-5 py-4">
        <h3 className="text-[10px] font-medium">Exported variables</h3>
        <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
          {description}
        </p>
      </header>
      <div className="grid grid-cols-[13rem_minmax(0,1fr)_3rem] border-b border-border px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
        <span>Name</span>
        <span>Current value</span>
        <span />
      </div>
      {variables.map((variable) => (
        <div
          className="grid min-h-11 grid-cols-[13rem_minmax(0,1fr)_3rem] items-center border-b border-border px-5 text-[10px]"
          key={variable.name}
        >
          <code>{variable.name}</code>
          <code
            className="truncate pr-4 text-muted-foreground"
            title={variable.value}
          >
            {variable.value}
          </code>
          <Button
            aria-label={`Copy ${variable.name}`}
            onClick={() => void copy(variable)}
            size="icon"
            variant="ghost"
          >
            {copied === variable.name ? <Check /> : <Copy />}
          </Button>
        </div>
      ))}
    </section>
  );
};
