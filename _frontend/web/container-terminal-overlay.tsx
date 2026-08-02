import { Play, RotateCcw, SquareTerminal, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";

import { fetchResourceTerminalShells } from "@/api";
import type { ContainerResourceKind } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { Terminal } from "@/terminal";
import type { TerminalConnection } from "@/terminal";
import { resourceTerminalSocketURL } from "@/terminal-url";

interface ContainerTerminalOverlayProperties {
  className?: string;
  embedded?: boolean;
  onClose?: () => void;
  projectID: string;
  resourceID: string;
  resourceKind: ContainerResourceKind;
  resourceName: string;
}

const parseCommand = (value: string) => {
  const parsed: unknown = JSON.parse(value);
  if (
    !Array.isArray(parsed) ||
    parsed.length === 0 ||
    parsed.length > 64 ||
    parsed.some((argument) => typeof argument !== "string" || !argument)
  ) {
    throw new Error("Command must be a JSON array of 1–64 non-empty strings");
  }
  return parsed as string[];
};

const connectionColors: Record<TerminalConnection, string> = {
  connected: "bg-emerald-400",
  connecting: "animate-pulse bg-amber-400",
  disconnected: "bg-rose-400",
};

export const ContainerTerminalOverlay = ({
  className,
  embedded = false,
  onClose,
  projectID,
  resourceID,
  resourceKind,
  resourceName,
}: ContainerTerminalOverlayProperties) => {
  const [shells, setShells] = useState<string[]>([]);
  const [selection, setSelection] = useState("custom");
  const [customCommand, setCustomCommand] = useState('["/bin/sh"]');
  const [activeCommand, setActiveCommand] = useState<string[]>();
  const [session, setSession] = useState(0);
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [connection, setConnection] =
    useState<TerminalConnection>("connecting");

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      setLoading(true);
      setError(undefined);
      setShells([]);
      setSelection("custom");
      setActiveCommand(undefined);
      setConnection("connecting");
      try {
        const available = await fetchResourceTerminalShells(
          projectID,
          resourceKind,
          resourceID,
          controller.signal
        );
        setShells(available);
        if (available[0]) {
          setSelection(available[0]);
          setActiveCommand([available[0]]);
        } else {
          setSelection("custom");
          setActiveCommand(undefined);
        }
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to inspect container shells"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, resourceID, resourceKind]);

  const socketURL = useCallback(
    (cols: number, rows: number) =>
      resourceTerminalSocketURL(
        projectID,
        resourceKind,
        resourceID,
        activeCommand ?? [],
        cols,
        rows
      ),
    [activeCommand, projectID, resourceID, resourceKind]
  );

  const start = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    try {
      const command =
        selection === "custom" ? parseCommand(customCommand) : [selection];
      setActiveCommand(command);
      setConnection("connecting");
      setSession((current) => current + 1);
      setError(undefined);
    } catch (commandError) {
      setError(
        commandError instanceof Error
          ? commandError.message
          : "Invalid terminal command"
      );
    }
  };

  const canStart =
    !loading && (selection !== "custom" || customCommand.trim().length > 0);
  let status = { color: "bg-muted-foreground/50", label: "idle" };
  if (activeCommand) {
    status = { color: connectionColors[connection], label: connection };
  } else if (loading) {
    status = { color: connectionColors.connecting, label: "inspecting" };
  }

  return (
    <SectionCard
      aria-label={`${resourceName} container terminal`}
      className={cn(
        "flex flex-col bg-background",
        embedded
          ? "h-[clamp(20rem,44vh,30rem)]"
          : "fixed inset-0 z-50 min-h-[36rem]",
        className
      )}
    >
      <form
        className="flex min-h-10 items-center gap-2 overflow-x-auto border-b border-border px-3 py-1.5"
        onSubmit={start}
      >
        <SquareTerminal className="size-4 shrink-0 text-muted-foreground" />
        <h2 className="max-w-48 shrink-0 truncate text-xs font-medium">
          {resourceName}
        </h2>
        <span className="hidden shrink-0 text-[8px] tracking-[0.12em] text-muted-foreground uppercase md:inline">
          Access only
        </span>
        <Select
          disabled={loading}
          items={[
            ...shells.map((shell) => ({ label: shell, value: shell })),
            { label: "Explicit argv", value: "custom" },
          ]}
          onValueChange={(value) => setSelection(String(value))}
          value={selection}
        >
          <SelectTrigger
            aria-label="Terminal shell"
            className="h-7 min-w-32 shrink-0 text-[10px]"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="start">
            {shells.map((shell) => (
              <SelectItem key={shell} value={shell}>
                {shell}
              </SelectItem>
            ))}
            <SelectItem value="custom">Explicit argv</SelectItem>
          </SelectContent>
        </Select>
        {selection === "custom" ? (
          <Input
            aria-label="Explicit terminal command as JSON argv"
            className="h-7 min-w-64 flex-1 font-mono text-[10px]"
            onChange={(event) => setCustomCommand(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                event.currentTarget.form?.requestSubmit();
              }
            }}
            spellCheck={false}
            value={customCommand}
          />
        ) : null}
        <Button
          className="shrink-0"
          disabled={!canStart}
          size="sm"
          type="submit"
          variant="ghost"
        >
          {activeCommand ? <RotateCcw /> : <Play />}
          {activeCommand ? "Restart" : "Start session"}
        </Button>
        <div className="ml-auto flex shrink-0 items-center gap-2 text-[9px] text-muted-foreground">
          <span className={cn("size-1.5", status.color)} />
          <span>{status.label}</span>
        </div>
        {onClose ? (
          <Button
            aria-label="Close terminal"
            className="shrink-0"
            onClick={onClose}
            size="icon"
            type="button"
            variant="ghost"
          >
            <X />
          </Button>
        ) : null}
      </form>

      {error ? (
        <div className="border-b border-rose-500/30 bg-rose-500/5 px-4 py-2 text-[10px] text-rose-600 dark:text-rose-300">
          {error}
        </div>
      ) : null}

      {activeCommand ? (
        <Terminal
          key={session}
          onConnectionChange={setConnection}
          showStatusBar={false}
          socketURL={socketURL}
        />
      ) : (
        <div className="grid min-h-0 flex-1 place-items-center bg-[#171615] px-8 text-center">
          <div className="max-w-lg">
            <SquareTerminal className="mx-auto size-5 text-[#6f6963]" />
            <p className="mt-3 text-xs text-[#d9d3cc]">
              {loading ? "Inspecting container…" : "No supported shell found"}
            </p>
            {loading ? null : (
              <p className="mt-2 text-[10px] leading-5 text-[#8d8780]">
                This image does not contain /bin/sh or /bin/bash. Explicit argv
                works only when that executable already exists in the container.
              </p>
            )}
          </div>
        </div>
      )}
    </SectionCard>
  );
};
