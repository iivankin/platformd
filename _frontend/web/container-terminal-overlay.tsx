import { Play, SquareTerminal, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { fetchServiceTerminalShells } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Terminal } from "@/terminal";
import { serviceTerminalSocketURL } from "@/terminal-url";

interface ContainerTerminalOverlayProperties {
  onClose: () => void;
  projectID: string;
  serviceID: string;
  serviceName: string;
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

export const ContainerTerminalOverlay = ({
  onClose,
  projectID,
  serviceID,
  serviceName,
}: ContainerTerminalOverlayProperties) => {
  const [shells, setShells] = useState<string[]>([]);
  const [selection, setSelection] = useState("custom");
  const [customCommand, setCustomCommand] = useState('["/bin/sh"]');
  const [activeCommand, setActiveCommand] = useState<string[]>();
  const [session, setSession] = useState(0);
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const available = await fetchServiceTerminalShells(
          projectID,
          serviceID,
          controller.signal
        );
        setShells(available);
        if (available[0]) {
          setSelection(available[0]);
          setActiveCommand([available[0]]);
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
        setLoading(false);
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, serviceID]);

  const socketURL = useCallback(
    (cols: number, rows: number) =>
      serviceTerminalSocketURL(
        projectID,
        serviceID,
        activeCommand ?? [],
        cols,
        rows
      ),
    [activeCommand, projectID, serviceID]
  );

  const start = () => {
    try {
      const command =
        selection === "custom" ? parseCommand(customCommand) : [selection];
      setActiveCommand(command);
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

  return (
    <section
      aria-label={`${serviceName} container terminal`}
      className="fixed inset-0 z-50 flex flex-col bg-background"
    >
      <header className="flex min-h-12 flex-wrap items-center gap-3 border-b border-border px-4 py-2">
        <SquareTerminal className="size-4 text-muted-foreground" />
        <div className="min-w-0">
          <h2 className="truncate text-xs font-medium">{serviceName}</h2>
          <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
            Container console · Access only
          </p>
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <select
            aria-label="Terminal shell"
            className="h-8 min-w-32 border border-input bg-background px-2 text-[10px] outline-none focus-visible:border-ring"
            disabled={loading}
            onChange={(event) => setSelection(event.target.value)}
            value={selection}
          >
            {shells.map((shell) => (
              <option key={shell} value={shell}>
                {shell}
              </option>
            ))}
            <option value="custom">Explicit argv</option>
          </select>
          {selection === "custom" ? (
            <Input
              aria-label="Explicit terminal command as JSON argv"
              className="h-8 w-72 font-mono text-[10px]"
              onChange={(event) => setCustomCommand(event.target.value)}
              value={customCommand}
            />
          ) : null}
          <Button
            disabled={loading}
            onClick={start}
            size="sm"
            variant="outline"
          >
            <Play />
            New session
          </Button>
          <Button
            aria-label="Close terminal"
            onClick={onClose}
            size="icon"
            variant="ghost"
          >
            <X />
          </Button>
        </div>
      </header>
      {error ? (
        <div className="border-b border-rose-500/30 bg-rose-500/5 px-4 py-2 text-[10px] text-rose-600 dark:text-rose-300">
          {error}
        </div>
      ) : null}
      {activeCommand ? (
        <Terminal key={session} socketURL={socketURL} />
      ) : (
        <div className="grid flex-1 place-items-center bg-[#191816] px-8 text-center text-xs text-[#8d8780]">
          {loading
            ? "Inspecting available shells…"
            : "No allowlisted shell found. Enter an explicit argv to start."}
        </div>
      )}
    </section>
  );
};
