import { RefreshCw, SquareTerminal } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ServerTerminalOverlay } from "@/server-terminal-overlay";
import { useSelfUpdate } from "@/use-self-update";

export const InfrastructureOperationsPage = () => {
  const [terminalOpen, setTerminalOpen] = useState(false);
  const {
    error: updateError,
    start,
    targetVersion,
    updating,
  } = useSelfUpdate();

  return (
    <div>
      <section className="flex flex-col gap-5 border-b border-border px-5 py-6 md:flex-row md:items-center">
        <div className="grid size-10 shrink-0 place-items-center bg-muted">
          <SquareTerminal className="size-4" />
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
            Server access
          </p>
          <p className="mt-1 text-sm font-medium">Root console</p>
          <p className="mt-2 max-w-2xl text-[10px] leading-4 text-muted-foreground">
            Open a temporary terminal for server maintenance.
          </p>
        </div>
        <Button
          className="shrink-0"
          onClick={() => setTerminalOpen(true)}
          type="button"
          variant="outline"
        >
          <SquareTerminal />
          Open console
        </Button>
      </section>

      <section className="flex flex-col gap-5 border-b border-border px-5 py-6 md:flex-row md:items-center">
        <div className="grid size-10 shrink-0 place-items-center bg-muted">
          <RefreshCw className={cn("size-4", updating && "animate-spin")} />
        </div>
        <div className="min-w-0 flex-1">
          <p className="text-[9px] tracking-[0.15em] text-muted-foreground uppercase">
            Platform release
          </p>
          <p className="mt-1 text-sm font-medium">
            {targetVersion
              ? `Restarting into ${targetVersion}`
              : "Update platformd"}
          </p>
          <p className="mt-2 max-w-2xl text-[10px] leading-4 text-muted-foreground">
            Install the latest verified release and restart the platform.
          </p>
          {updateError ? (
            <p className="mt-2 text-[10px] text-rose-600 dark:text-rose-300">
              {updateError}
            </p>
          ) : null}
        </div>
        <Button
          className="shrink-0"
          disabled={updating}
          onClick={() => void start()}
          type="button"
        >
          {updating ? "Waiting for restart" : "Update platform"}
        </Button>
      </section>

      {terminalOpen ? (
        <ServerTerminalOverlay onClose={() => setTerminalOpen(false)} />
      ) : null}
    </div>
  );
};
