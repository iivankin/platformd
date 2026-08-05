import { Dialog } from "@base-ui/react/dialog";
import { ClipboardPaste, X } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { Button } from "@/components/ui/button";
import { parseServiceEnvironment } from "@/service-environment";

export const ServicePasteEnvDialog = ({
  busy,
  onPaste,
}: {
  busy: boolean;
  onPaste: (environment: Record<string, string>) => void;
}) => {
  const [open, setOpen] = useState(false);
  const [text, setText] = useState("");
  const [error, setError] = useState<string>();

  const reset = () => {
    setText("");
    setError(undefined);
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    try {
      const environment = parseServiceEnvironment(text);
      if (Object.keys(environment).length === 0) {
        setError("Paste at least one KEY=VALUE line.");
        return;
      }
      onPaste(environment);
      setOpen(false);
      reset();
    } catch (parseError) {
      setError(
        parseError instanceof Error
          ? parseError.message
          : "Unable to parse .env content"
      );
    }
  };

  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (!nextOpen) {
          reset();
        }
      }}
      open={open}
    >
      <Dialog.Trigger
        render={
          <Button disabled={busy} size="sm" variant="outline">
            <ClipboardPaste /> Paste from .env
          </Button>
        }
      />
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
        <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
          <Dialog.Popup className="flex w-full max-w-xl flex-col border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out">
            <header className="flex items-start justify-between gap-5 border-b border-border px-5 py-4">
              <div>
                <Dialog.Title className="text-sm font-medium">
                  Paste from .env
                </Dialog.Title>
                <Dialog.Description className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
                  Existing names are updated. New names are added. Blank lines
                  and <code>#</code> comments are ignored.
                </Dialog.Description>
              </div>
              <Dialog.Close
                aria-label="Close"
                className="flex size-8 shrink-0 items-center justify-center text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring"
              >
                <X className="size-4" />
              </Dialog.Close>
            </header>

            <form onSubmit={submit}>
              <label className="block px-5 py-4">
                <span className="text-[10px] font-medium">.env contents</span>
                <textarea
                  autoCapitalize="none"
                  autoComplete="off"
                  className="mt-2 min-h-48 w-full resize-y border border-input bg-transparent p-3 font-mono text-[10px] outline-none focus:border-ring"
                  onChange={(event) => {
                    setText(event.target.value);
                    setError(undefined);
                  }}
                  placeholder={"DATABASE_URL=postgres://...\nHF_TOKEN=..."}
                  spellCheck={false}
                  value={text}
                />
              </label>

              <footer className="flex items-center justify-end gap-3 border-t border-border bg-muted/15 px-5 py-3">
                {error ? (
                  <p className="mr-auto text-[10px] text-destructive">
                    {error}
                  </p>
                ) : null}
                <Dialog.Close
                  render={<Button size="sm" type="button" variant="ghost" />}
                >
                  Cancel
                </Dialog.Close>
                <Button
                  disabled={busy || text.trim() === ""}
                  size="sm"
                  type="submit"
                >
                  Add variables
                </Button>
              </footer>
            </form>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
};
