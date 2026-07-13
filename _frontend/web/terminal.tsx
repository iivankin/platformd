import type { FitAddon as GhosttyFitAddon } from "ghostty-web";
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
  const dark = document.documentElement.classList.contains("dark");
  return dark
    ? {
        background: "#171615",
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
        cursorAccent: "#171615",
        cyan: "#6fc9c4",
        foreground: "#e7e2dc",
        green: "#66c99a",
        magenta: "#d58ac5",
        red: "#ff6b64",
        selectionBackground: "#425b58",
        selectionForeground: "#fffaf4",
        white: "#d9d3cc",
        yellow: "#e8bd69",
      }
    : {
        background: "#191816",
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
        cursorAccent: "#191816",
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
  socketProtocols?: readonly string[];
  socketURL: (cols: number, rows: number) => string;
  visible?: boolean;
}

export const Terminal = ({
  socketProtocols,
  socketURL,
  visible = true,
}: TerminalProperties) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const fitRef = useRef<GhosttyFitAddon>(null);
  const [connection, setConnection] = useState<
    "connected" | "connecting" | "disconnected"
  >("connecting");
  const [error, setError] = useState<string>();
  const [painted, setPainted] = useState(false);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }
    let disposed = false;
    let hasPainted = false;
    let socket: WebSocket | undefined;
    let disposeTerminal: (() => void) | undefined;

    const start = async () => {
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

        socket = new WebSocket(
          socketURL(terminal.cols, terminal.rows),
          socketProtocols ? [...socketProtocols] : undefined
        );
        socket.binaryType = "arraybuffer";
        socket.addEventListener("open", () => {
          if (!disposed) {
            setConnection("connected");
            terminal.focus();
          }
        });
        socket.addEventListener("message", (event) => {
          if (!(event.data instanceof ArrayBuffer)) {
            socket?.close(1003, "binary terminal output required");
            return;
          }
          terminal.write(new Uint8Array(event.data), () => {
            if (hasPainted) {
              return;
            }
            hasPainted = true;
            requestAnimationFrame(() => {
              if (!disposed) {
                setPainted(true);
              }
            });
          });
        });
        socket.addEventListener("close", () => {
          if (!disposed) {
            setConnection("disconnected");
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
      socket?.close(1000, "terminal closed");
      disposeTerminal?.();
    };
  }, [socketProtocols, socketURL]);

  useEffect(() => {
    if (!visible) {
      return;
    }
    const frame = requestAnimationFrame(() => fitRef.current?.fit());
    return () => cancelAnimationFrame(frame);
  }, [visible]);

  return (
    <div className="relative flex min-h-0 flex-1 overflow-hidden bg-[#191816]">
      <div className="absolute top-2 right-3 z-10 flex items-center gap-1.5 bg-[#191816]/90 px-1.5 py-1 text-[9px] text-[#8d8780]">
        <span className={`size-1.5 ${connectionColors[connection]}`} />
        {connection}
      </div>
      <div
        className={`min-h-0 min-w-0 flex-1 overflow-hidden py-3 pl-3 transition-opacity duration-150 [&_canvas]:block ${
          painted ? "opacity-100" : "opacity-0"
        }`}
        ref={containerRef}
      />
      {error ? (
        <div className="absolute inset-0 grid place-items-center bg-[#191816] px-8 text-center text-xs text-rose-300">
          {error}
        </div>
      ) : null}
    </div>
  );
};
