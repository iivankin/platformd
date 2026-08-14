export interface MockSocketData {
  kind: "terminal";
}

const encoder = new TextEncoder();
const decoder = new TextDecoder();

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
    socket.send(
      encoder.encode(
        "\r\nplatformd mock terminal\r\nCommands are echoed and never executed.\r\nmock $ "
      )
    );
  },
};
