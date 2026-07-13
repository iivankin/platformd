import { expect, test } from "bun:test";

import {
  serverTerminalSocketURL,
  serviceTerminalSocketURL,
} from "@/terminal-url";

test("builds bounded terminal websocket URLs without credentials", () => {
  expect(
    serviceTerminalSocketURL(
      "project/id",
      "service/id",
      ["/bin/sh"],
      2000,
      0,
      "https://admin.example.com"
    )
  ).toBe(
    "wss://admin.example.com/api/v1/projects/project%2Fid/services/service%2Fid/terminal?cols=1000&rows=1&arg=%2Fbin%2Fsh"
  );
  expect(serverTerminalSocketURL(120, 40, "https://admin.example.com")).toBe(
    "wss://admin.example.com/api/v1/server/terminal?cols=120&rows=40"
  );
});
