import type { LogWindow } from "../web/api";
import type { MockState } from "./state";

export type MockSocketData =
  | { kind: "logs"; window: LogWindow }
  | { kind: "terminal" };

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export const upgradeLogSocket = (
  request: Request,
  server: Bun.Server<MockSocketData>,
  state: MockState,
  serviceID: string
) =>
  server.upgrade(request, {
    data: {
      kind: "logs",
      window: state.logs[serviceID] ?? { records: [], truncated: false },
    },
  });

export const upgradeTerminalSocket = (
  request: Request,
  server: Bun.Server<MockSocketData>
) => {
  const offeredProtocols =
    request.headers.get("Sec-WebSocket-Protocol")?.split(",") ?? [];
  const terminalProtocol = offeredProtocols
    .map((protocol) => protocol.trim())
    .find((protocol) => protocol === "platformd-terminal-v1");
  return server.upgrade(request, {
    data: { kind: "terminal" },
    ...(terminalProtocol
      ? { headers: { "Sec-WebSocket-Protocol": terminalProtocol } }
      : {}),
  });
};

export const mockWebSocketHandlers: Bun.WebSocketHandler<MockSocketData> = {
  message(socket, message) {
    if (socket.data.kind !== "terminal") {
      return;
    }
    if (typeof message === "string") {
      if (message.includes('"type":"resize"')) {
        return;
      }
      socket.send(encoder.encode(message));
      return;
    }
    const input = decoder.decode(message);
    socket.send(encoder.encode(input));
  },
  open(socket) {
    if (socket.data.kind === "logs") {
      socket.send(
        JSON.stringify({
          records: socket.data.window.records,
          truncated: socket.data.window.truncated,
          type: "snapshot",
        })
      );
      return;
    }
    socket.send(
      encoder.encode(
        "\r\nplatformd mock terminal\r\nCommands are echoed and never executed.\r\nmock $ "
      )
    );
  },
};
