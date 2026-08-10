import { handleErrorTrackerMock } from "./mock/error-tracker-router";
import { createErrorTrackerMockState } from "./mock/error-tracker-state";
import app from "./web/error-tracker/index.html";

const state = createErrorTrackerMockState();
const hostname = process.env.HOST ?? "127.0.0.1";
const port = Number(process.env.PORT ?? 3101);

const server = Bun.serve({
  development:
    process.env.NODE_ENV === "production"
      ? false
      : { console: true, hmr: true },
  hostname,
  port,
  routes: {
    "/*": app,
    "/api/v1/*": (request) => handleErrorTrackerMock(request, state),
  },
});

console.log(`error tracker mock: ${server.url}`);
console.log("mock state resets when the server restarts");

const shutdown = async () => {
  await server.stop(true);
  process.exit(0);
};

process.once("SIGINT", () => void shutdown());
process.once("SIGTERM", () => void shutdown());
