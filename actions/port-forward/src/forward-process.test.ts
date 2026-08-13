import assert from "node:assert/strict";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { removeWorkDir, startForward, stopForward, waitForReady } from "./forward-process.js";

test("starts, observes, and stops the detached forward process", async (t) => {
  const workDir = await mkdtemp(join(tmpdir(), "platformd-port-forward-"));
  let pid: number | undefined;
  t.after(async () => {
    if (pid) {
      await stopForward(pid);
    }
    await removeWorkDir(workDir, tmpdir());
  });
  const fakeForward = join(workDir, "fake-forward");
  await writeFile(
    fakeForward,
    [
      "#!/usr/bin/env node",
      "const port = process.argv[process.argv.indexOf('--local-port') + 1];",
      "console.error(`Forwarding 127.0.0.1:${port} to the platformd resource`);",
      "process.on('SIGTERM', () => process.exit(0));",
      "setInterval(() => {}, 1000);",
    ].join("\n"),
    { mode: 0o700 },
  );

  const forward = await startForward({
    binaryPath: fakeForward,
    websocketUrl: "wss://admin.example.com/public/api/v1/port-forward",
    ticket: "pft_secret",
    localPort: 15432,
    httpHost: "",
    workDir,
  });
  pid = forward.child.pid ?? undefined;
  await waitForReady(forward.child, forward.logPath, 15432);
  assert.ok(pid !== undefined && pid > 1);
});
