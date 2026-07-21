import { SquareTerminal } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
import { PlatformReleaseOperation } from "@/platform-release-operation";
import { ServerTerminalOverlay } from "@/server-terminal-overlay";
import type { UpdateStatusState } from "@/use-update-status";

export const InfrastructureOperationsPage = ({
  update,
}: {
  update: UpdateStatusState;
}) => {
  const [terminalOpen, setTerminalOpen] = useState(false);

  return (
    <PageStack>
      <SectionCard className="flex flex-col gap-5 px-5 py-6 md:flex-row md:items-center">
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
      </SectionCard>

      <PlatformReleaseOperation update={update} />

      {terminalOpen ? (
        <ServerTerminalOverlay onClose={() => setTerminalOpen(false)} />
      ) : null}
    </PageStack>
  );
};
