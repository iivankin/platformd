import { Dialog } from "@base-ui/react/dialog";
import { Check, Clipboard, LoaderCircle, X } from "lucide-react";
import { useState } from "react";
import type { ReactElement, ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import type { SecretValue } from "./types";

export const Modal = ({
  children,
  className,
  description,
  onOpenChange,
  open,
  title,
  trigger,
}: {
  children: ReactNode;
  className?: string;
  description: string;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  title: string;
  trigger?: ReactElement;
}) => (
  <Dialog.Root onOpenChange={onOpenChange} open={open}>
    {trigger ? <Dialog.Trigger render={trigger} /> : null}
    <Dialog.Portal>
      <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
      <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
        <Dialog.Popup
          className={cn(
            "max-h-[calc(100dvh-2rem)] w-full max-w-lg overflow-y-auto border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out",
            className
          )}
        >
          <header className="flex items-start justify-between gap-5 border-b border-border px-5 py-4">
            <div>
              <Dialog.Title className="text-sm font-medium">
                {title}
              </Dialog.Title>
              <Dialog.Description className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
                {description}
              </Dialog.Description>
            </div>
            <Dialog.Close
              aria-label="Close dialog"
              className="grid size-8 shrink-0 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground"
            >
              <X className="size-4" />
            </Dialog.Close>
          </header>
          {children}
        </Dialog.Popup>
      </Dialog.Viewport>
    </Dialog.Portal>
  </Dialog.Root>
);

export const SecretReveal = ({ values }: { values: SecretValue[] }) => {
  const [copied, setCopied] = useState("");
  const copy = async (item: SecretValue) => {
    await navigator.clipboard.writeText(item.value);
    setCopied(item.label);
  };
  return (
    <div className="bg-emerald-500/5 px-5 py-5">
      <div className="flex items-start gap-3">
        <Check className="mt-0.5 size-4 shrink-0 text-emerald-600" />
        <div>
          <p className="text-xs font-medium">Save these values now</p>
          <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
            One-time secrets cannot be recovered after this dialog closes.
          </p>
        </div>
      </div>
      <div className="mt-4 divide-y divide-border border-y border-border">
        {values.map((item) => (
          <div className="grid gap-2 py-3" key={item.label}>
            <div className="flex items-center justify-between gap-3">
              <span className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
                {item.label}
              </span>
              <Button onClick={() => void copy(item)} size="sm" variant="ghost">
                {copied === item.label ? <Check /> : <Clipboard />}
                {copied === item.label ? "Copied" : "Copy"}
              </Button>
            </div>
            <code className="overflow-x-auto border border-emerald-500/25 bg-background px-3 py-2 text-[10px] leading-5 select-all">
              {item.value}
            </code>
          </div>
        ))}
      </div>
      <div className="mt-4 flex justify-end">
        <Dialog.Close render={<Button>Done</Button>} />
      </div>
    </div>
  );
};

export const FormFooter = ({
  error,
  label,
  pending,
}: {
  error: string;
  label: string;
  pending: boolean;
}) => (
  <>
    {error ? (
      <p className="border-t border-destructive/20 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
        {error}
      </p>
    ) : null}
    <footer className="flex items-center justify-end gap-2 border-t border-border px-5 py-4">
      <Dialog.Close
        render={
          <Button type="button" variant="ghost">
            Cancel
          </Button>
        }
      />
      <Button disabled={pending} type="submit">
        {pending ? <LoaderCircle className="animate-spin" /> : null}
        {pending ? "Saving…" : label}
      </Button>
    </footer>
  </>
);
