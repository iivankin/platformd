import type { FitAddon as GhosttyFitAddon } from "ghostty-web";
import { SquareTerminal } from "lucide-react";
import { useEffect, useRef, useState } from "react";

const initializeGhostty = async () => {
  const module = await import("ghostty-web");
  await module.init();
  return module;
};

let ghosttyModulePromise: ReturnType<typeof initializeGhostty> | undefined;

const loadGhostty = () => {
  ghosttyModulePromise ??= initializeGhostty();
  return ghosttyModulePromise;
};

const encoder = new TextEncoder();
const connectionColors = {
  connected: "bg-emerald-400",
  connecting: "animate-pulse bg-amber-400",
  disconnected: "bg-rose-400",
} as const;

const theme = () => {
  const background = document.documentElement.classList.contains("dark")
    ? "#171615"
    : "#191816";
  return {
    background,
    black: "#262321",
    blue: "#78a9e8",
    brightBlack: "#746e68",
    brightBlue: "#91baf0",
    brightCyan: "#8addd7",
    brightGreen: "#82dbad",
    brightMagenta: "#e2a2d4",
    brightRed: "#ff8a83",
    brightWhite: "#fffaf4",
    brightYellow: "#f3ce7d",
    cursor: "#f0ece7",
    cursorAccent: background,
    cyan: "#6fc9c4",
    foreground: "#e7e2dc",
    green: "#66c99a",
    magenta: "#d58ac5",
    red: "#ff6b64",
    selectionBackground: "#425b58",
    selectionForeground: "#fffaf4",
    white: "#d9d3cc",
    yellow: "#e8bd69",
  };
};

interface TerminalProperties {
  onConnectionChange?: (connection: TerminalConnection) => void;
  showStatusBar?: boolean;
  socketProtocols?: readonly string[];
  socketURL: (cols: number, rows: number) => string;
  title?: string;
  visible?: boolean;
}

export type TerminalConnection = "connected" | "connecting" | "disconnected";

export const Terminal = ({
  onConnectionChange,
  showStatusBar = true,
  socketProtocols,
  socketURL,
  title = "PTY session",
  visible = true,
}: TerminalProperties) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const fitRef = useRef<GhosttyFitAddon>(null);
  const [connection, setConnection] = useState<
    "connected" | "connecting" | "disconnected"
  >("connecting");
  const [disconnectReason, setDisconnectReason] = useState<string>();
  const [error, setError] = useState<string>();
  const [hasOutput, setHasOutput] = useState(false);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }
    let disposed = false;
    let hasReceivedOutput = false;
    let socket: WebSocket | undefined;
    let disposeTerminal: (() => void) | undefined;
    let focusTimer: ReturnType<typeof setTimeout> | undefined;

    const start = async () => {
      setConnection("connecting");
      setDisconnectReason(undefined);
      setError(undefined);
      setHasOutput(false);
      setReady(false);
      try {
        const { FitAddon, Terminal: GhosttyTerminal } = await loadGhostty();
        if (disposed) {
          return;
        }
        const terminal = new GhosttyTerminal({
          cursorBlink: true,
          cursorStyle: "bar",
          fontFamily: '"JetBrains Mono Variable", monospace',
          fontSize: 12,
          scrollback: 10_000,
          theme: theme(),
        });
        const fit = new FitAddon();
        fitRef.current = fit;
        terminal.loadAddon(fit);
        terminal.open(container);
        fit.fit();
        fit.observeResize();
        setReady(true);

        socket = new WebSocket(
          socketURL(terminal.cols, terminal.rows),
          socketProtocols ? [...socketProtocols] : undefined
        );
        socket.binaryType = "arraybuffer";
        socket.addEventListener("open", () => {
          if (!disposed) {
            setConnection("connected");
            terminal.focus();
            terminal.textarea?.focus();
            focusTimer = setTimeout(() => {
              if (!disposed) {
                terminal.textarea?.focus();
              }
            }, 0);
          }
        });
        socket.addEventListener("message", (event) => {
          if (!(event.data instanceof ArrayBuffer)) {
            socket?.close(1003, "binary terminal output required");
            return;
          }
          terminal.write(new Uint8Array(event.data), () => {
            if (hasReceivedOutput) {
              return;
            }
            hasReceivedOutput = true;
            requestAnimationFrame(() => {
              if (!disposed) {
                setHasOutput(true);
              }
            });
          });
        });
        socket.addEventListener("close", (event) => {
          if (!disposed) {
            setConnection("disconnected");
            setDisconnectReason(
              event.reason ||
                (event.code === 1000
                  ? "Session ended"
                  : "Session ended unexpectedly")
            );
          }
        });
        socket.addEventListener("error", () => {
          if (!disposed) {
            setConnection("disconnected");
          }
        });

        const dataListener = terminal.onData((data) => {
          if (socket?.readyState === WebSocket.OPEN) {
            socket.send(encoder.encode(data));
          }
        });
        const resizeListener = terminal.onResize(({ cols, rows }) => {
          if (socket?.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ cols, rows, type: "resize" }));
          }
        });
        const handleCopy = (event: ClipboardEvent) => {
          const selection = terminal.getSelection();
          if (!(selection && event.clipboardData)) {
            return;
          }
          event.preventDefault();
          event.clipboardData.setData("text/plain", selection);
        };
        const handlePaste = (event: ClipboardEvent) => {
          const text = event.clipboardData?.getData("text/plain");
          if (!text) {
            return;
          }
          event.preventDefault();
          terminal.paste(text);
        };
        container.addEventListener("copy", handleCopy);
        container.addEventListener("paste", handlePaste);
        disposeTerminal = () => {
          container.removeEventListener("copy", handleCopy);
          container.removeEventListener("paste", handlePaste);
          dataListener.dispose();
          resizeListener.dispose();
          terminal.renderer?.clear();
          fit.dispose();
          terminal.dispose();
          if (fitRef.current === fit) {
            fitRef.current = null;
          }
        };
      } catch (loadError) {
        if (!disposed) {
          setConnection("disconnected");
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to initialize terminal"
          );
        }
      }
    };
    void start();
    return () => {
      disposed = true;
      if (focusTimer !== undefined) {
        clearTimeout(focusTimer);
      }
      socket?.close(1000, "terminal closed");
      disposeTerminal?.();
    };
  }, [socketProtocols, socketURL]);

  useEffect(() => {
    onConnectionChange?.(connection);
  }, [connection, onConnectionChange]);

  useEffect(() => {
    if (!visible) {
      return;
    }
    const frame = requestAnimationFrame(() => fitRef.current?.fit());
    return () => cancelAnimationFrame(frame);
  }, [visible]);

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden bg-[#171615]">
      {showStatusBar ? (
        <div className="flex h-7 shrink-0 items-center gap-2 border-b border-[#2b2926] px-3 text-[9px] text-[#8d8780]">
          <SquareTerminal className="size-3" />
          <code className="min-w-0 flex-1 truncate text-[#b7b1aa]">
            {title}
          </code>
          <span className={`size-1.5 ${connectionColors[connection]}`} />
          <span>{disconnectReason ?? connection}</span>
        </div>
      ) : null}
      <div className="relative min-h-0 flex-1 overflow-hidden">
        <div
          className="h-full min-h-0 min-w-0 overflow-hidden px-3 py-2.5 caret-transparent [&_canvas]:block"
          ref={containerRef}
        />
        {!ready && !error ? (
          <div className="absolute inset-0 grid place-items-center bg-[#171615] px-8 text-center text-[10px] text-[#8d8780]">
            Starting PTY…
          </div>
        ) : null}
        {connection === "disconnected" && !hasOutput && !error ? (
          <div className="absolute inset-0 grid place-items-center bg-[#171615] px-8 text-center">
            <div>
              <p className="text-xs text-[#d9d3cc]">Session did not start</p>
              <p className="mt-2 text-[10px] text-[#8d8780]">
                {disconnectReason ?? "The terminal connection was closed"}
              </p>
            </div>
          </div>
        ) : null}
        {error ? (
          <div className="absolute inset-0 grid place-items-center bg-[#171615] px-8 text-center text-xs text-rose-300">
            {error}
          </div>
        ) : null}
      </div>
    </div>
  );
};
