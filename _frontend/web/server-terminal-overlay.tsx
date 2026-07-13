import {
  KeyRound,
  RotateCcw,
  ShieldAlert,
  SquareTerminal,
  X,
} from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import type { FormEvent } from "react";

import { issueServerTerminalToken } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Terminal } from "@/terminal";
import { serverTerminalSocketURL } from "@/terminal-url";

const terminalProtocol = "platformd-terminal-v1";
const bearerProtocolPrefix = "platformd-bearer.";

interface ServerTerminalOverlayProperties {
  onClose: () => void;
}

export const ServerTerminalOverlay = ({
  onClose,
}: ServerTerminalOverlayProperties) => {
  const [passphrase, setPassphrase] = useState("");
  const [token, setToken] = useState<string>();
  const [session, setSession] = useState(0);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string>();
  const socketURL = useCallback(
    (cols: number, rows: number) => serverTerminalSocketURL(cols, rows),
    []
  );
  const socketProtocols = useMemo(
    () =>
      token ? [terminalProtocol, `${bearerProtocolPrefix}${token}`] : undefined,
    [token]
  );

  const authorize = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!passphrase || submitting) {
      return;
    }
    setSubmitting(true);
    setError(undefined);
    try {
      const issued = await issueServerTerminalToken(passphrase);
      setToken(issued.token);
      setSession((current) => current + 1);
    } catch (authorizeError) {
      setError(
        authorizeError instanceof Error
          ? authorizeError.message
          : "Unable to authorize server console"
      );
    } finally {
      setPassphrase("");
      setSubmitting(false);
    }
  };

  const endSession = () => {
    setToken(undefined);
    setError(undefined);
  };

  return (
    <section
      aria-label="Server root console"
      className="fixed inset-0 z-50 flex flex-col bg-background"
    >
      <header className="flex min-h-12 flex-wrap items-center gap-3 border-b border-border px-4 py-2">
        <SquareTerminal className="size-4 text-muted-foreground" />
        <div className="min-w-0">
          <h2 className="truncate text-xs font-medium">Server console</h2>
          <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
            Host root PTY · Access + passphrase
          </p>
        </div>
        <div className="ml-auto flex items-center gap-2">
          {token ? (
            <Button onClick={endSession} size="sm" variant="outline">
              <RotateCcw />
              End session
            </Button>
          ) : null}
          <Button
            aria-label="Close server console"
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

      {token && socketProtocols ? (
        <Terminal
          key={session}
          socketProtocols={socketProtocols}
          socketURL={socketURL}
        />
      ) : (
        <div className="grid min-h-0 flex-1 place-items-center bg-[#191816] px-6 py-12">
          <form
            className="w-full max-w-md border-y border-[#37332f] py-7"
            onSubmit={(event) => void authorize(event)}
          >
            <ShieldAlert className="size-5 text-amber-300" />
            <h3 className="mt-4 text-sm font-medium text-[#f0ece7]">
              Full root access
            </h3>
            <p className="mt-2 text-[10px] leading-5 text-[#9b958e]">
              Commands can read platform secrets, modify the host, and stop all
              workloads. The passphrase is verified only for this console
              opening and is never persisted by the UI.
            </p>
            <label
              className="mt-5 block text-[9px] tracking-[0.12em] text-[#9b958e] uppercase"
              htmlFor="server-console-passphrase"
            >
              Console passphrase
            </label>
            <div className="mt-2 flex gap-2">
              <Input
                autoComplete="current-password"
                autoFocus
                className="border-[#49443f] bg-[#211f1d] text-[#f0ece7]"
                id="server-console-passphrase"
                onChange={(event) => setPassphrase(event.target.value)}
                type="password"
                value={passphrase}
              />
              <Button disabled={!passphrase || submitting} type="submit">
                <KeyRound />
                {submitting ? "Verifying…" : "Open"}
              </Button>
            </div>
          </form>
        </div>
      )}
    </section>
  );
};
