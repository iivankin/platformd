import { handleMockAPI } from "./mock/router";
import { createMockState } from "./mock/state";
import type { MockScenario } from "./mock/state";
import {
  mockWebSocketHandlers,
  upgradeLogSocket,
  upgradeTerminalSocket,
} from "./mock/websocket";
import type { MockSocketData } from "./mock/websocket";
import app from "./web/index.html";

const scenarios = new Set<MockScenario>(["demo", "empty", "error"]);
const scenarioIndex = process.argv.indexOf("--scenario");
const requestedScenario =
  scenarioIndex === -1
    ? process.env.MOCK_SCENARIO
    : process.argv[scenarioIndex + 1];
const scenario = requestedScenario ?? "demo";

if (!scenarios.has(scenario as MockScenario)) {
  console.error(
    `Unknown mock scenario "${scenario}". Use demo, empty, or error.`
  );
  process.exit(1);
}

const state = createMockState(scenario as MockScenario);
const hostname = process.env.HOST ?? "127.0.0.1";
const port = Number(process.env.PORT ?? 3100);

const server = Bun.serve<MockSocketData>({
  development:
    process.env.NODE_ENV === "production"
      ? false
      : { console: true, hmr: true },
  hostname,
  port,
  routes: {
    "/*": app,
    "/api/v1/*": (request: Bun.BunRequest<"/api/v1/*">) =>
      handleMockAPI(request, state),
    "/api/v1/projects/:projectID/resources/:resourceKind/:resourceID/terminal":
      (
        request: Bun.BunRequest<"/api/v1/projects/:projectID/resources/:resourceKind/:resourceID/terminal">,
        bunServer: Bun.Server<MockSocketData>
      ) => {
        if (upgradeTerminalSocket(request, bunServer)) {
          return;
        }
        return new Response("WebSocket upgrade required", { status: 426 });
      },
    "/api/v1/projects/:projectID/services/:serviceID/logs/stream": (
      request: Bun.BunRequest<"/api/v1/projects/:projectID/services/:serviceID/logs/stream">,
      bunServer: Bun.Server<MockSocketData>
    ) => {
      if (
        upgradeLogSocket(
          request,
          bunServer,
          state,
          decodeURIComponent(request.params.serviceID)
        )
      ) {
        return;
      }
      return new Response("WebSocket upgrade required", { status: 426 });
    },
    "/api/v1/server/terminal": (
      request: Bun.BunRequest<"/api/v1/server/terminal">,
      bunServer: Bun.Server<MockSocketData>
    ) => {
      if (upgradeTerminalSocket(request, bunServer)) {
        return;
      }
      return new Response("WebSocket upgrade required", { status: 426 });
    },
    "/cdn-cgi/access/get-identity": () =>
      Response.json({
        email: state.identity.email,
        name: state.identity.name,
      }),
  },
  websocket: mockWebSocketHandlers,
});

console.log(`platformd UI: ${server.url}`);
console.log(
  `mock scenario: ${scenario} (state resets when the server restarts)`
);

const shutdown = async () => {
  await server.stop(true);
  process.exit(0);
};

process.once("SIGINT", () => void shutdown());
process.once("SIGTERM", () => void shutdown());
