import { describe, expect, test } from "bun:test";

import type { LogRecord } from "@/api";
import {
  applyLogStreamMessage,
  serviceLogDownloadURL,
  serviceLogSocketURL,
} from "@/log-stream";

const record = (text: string): LogRecord => ({
  attemptId: "attempt",
  deploymentId: "deployment",
  stream: "stdout",
  text,
  timestamp: "2026-07-13T12:00:00Z",
});

test("builds a bounded log download URL", () => {
  expect(
    serviceLogDownloadURL(
      "project/a",
      "service",
      { deploymentId: "deployment", from: 10, to: 20 },
      "https://admin.example.com"
    )
  ).toBe(
    "https://admin.example.com/api/v1/projects/project%2Fa/services/service/logs/download?from=10&to=20&deploymentId=deployment"
  );
});

describe("log stream", () => {
  test("builds a bounded websocket URL", () => {
    expect(
      serviceLogSocketURL(
        "project/a",
        "service",
        { contains: "ready", deploymentId: "deployment", limit: 500 },
        "https://admin.example.com"
      )
    ).toBe(
      "wss://admin.example.com/api/v1/projects/project%2Fa/services/service/logs/stream?limit=500&deploymentId=deployment&contains=ready"
    );
  });

  test("replaces snapshots, bounds updates, and records gaps", () => {
    const snapshot = applyLogStreamMessage(
      undefined,
      { records: [record("one")], truncated: false, type: "snapshot" },
      2
    );
    const updated = applyLogStreamMessage(
      snapshot,
      { records: [record("two"), record("three")], type: "records" },
      2
    );
    expect(updated.records.map(({ text }) => text)).toEqual(["two", "three"]);
    expect(updated.truncated).toBe(true);
    expect(applyLogStreamMessage(updated, { type: "gap" }, 2).truncated).toBe(
      true
    );
  });
});
