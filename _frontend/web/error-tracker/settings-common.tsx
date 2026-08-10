import { Check, Clipboard } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";

import { errorMessage } from "./format";

export const SettingsSection = ({
  children,
  copy,
  showDivider = true,
  title,
}: {
  children: React.ReactNode;
  copy: string;
  showDivider?: boolean;
  title: string;
}) => (
  <section
    className={
      showDivider
        ? "border-b border-border px-5 py-6 lg:px-7"
        : "px-5 py-6 lg:px-7"
    }
  >
    <div className="max-w-4xl">
      <h2 className="text-sm font-medium">{title}</h2>
      <p className="mt-1.5 max-w-3xl text-[10px] leading-5 text-muted-foreground">
        {copy}
      </p>
      <div className="mt-4">{children}</div>
    </div>
  </section>
);

export const CopyButton = ({
  notify,
  value,
}: {
  notify: (message: string) => void;
  value: string;
}) => {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      notify("Copied to clipboard");
    } catch (error) {
      notify(errorMessage(error, "Clipboard access failed"));
    }
  };
  return (
    <Button onClick={() => void copy()} size="sm" variant="outline">
      {copied ? <Check /> : <Clipboard />}
      {copied ? "Copied" : "Copy"}
    </Button>
  );
};
