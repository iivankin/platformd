import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import {
  createPortForward,
  downloadForward,
  prepareForwardBinary,
  validWebSocketUrl,
  verifyChecksum,
} from "./platformd.js";

test("creates a ticket using the platformd public API", async () => {
  let request: { url: string; options: RequestInit } | undefined;
  const config = {
    baseUrl: "https://admin.example.com",
    token: "admin-token",
    project: "project-name",
    resource: "database-name",
    port: 5432,
    localPort: 15432,
    expiresInSeconds: 900,
  };
  const grant = await createPortForward(config, async (url, options) => {
    request = { url: String(url), options: options ?? {} };
    return new Response(
      JSON.stringify({
        ticket: "pft_ticket",
        project: "project-name",
        resource: "database-name",
        resourceKind: "postgres",
        expiresAt: "2026-07-26T12:00:00Z",
        instructions: { websocketUrl: "wss://admin.example.com/public/api/v1/port-forward" },
      }),
      { status: 201, headers: { "content-type": "application/json" } },
    );
  });

  assert.ok(request);
  assert.equal(
    request.url,
    "https://admin.example.com/public/api/v1/projects/project-name/resources/database-name/port-forwards",
  );
  const headers = new Headers(request.options.headers);
  assert.equal(headers.get("authorization"), "Bearer admin-token");
  assert.deepEqual(JSON.parse(String(request.options.body)), {
    port: 5432,
    localPort: 15432,
    expiresInSeconds: 900,
  });
  assert.deepEqual(grant, {
    ticket: "pft_ticket",
    websocketUrl: "wss://admin.example.com/public/api/v1/port-forward",
    expiresAt: "2026-07-26T12:00:00Z",
    resourceKind: "postgres",
  });
});

test("reports a safe API error without returning the response body", async () => {
  const config = {
    baseUrl: "https://admin.example.com",
    token: "admin-token",
    project: "project",
    resource: "backend",
    port: 8080,
    localPort: 8080,
    expiresInSeconds: 900,
  };
  await assert.rejects(
    createPortForward(
      config,
      async () =>
        new Response(
          JSON.stringify({ error: { code: "target_unavailable", message: "Target is not running" } }),
          { status: 409 },
        ),
    ),
    /HTTP 409: Target is not running/,
  );
});

test("validates the fixed WSS endpoint", () => {
  assert.equal(validWebSocketUrl("wss://admin.example.com/public/api/v1/port-forward"), true);
  assert.equal(validWebSocketUrl("ws://admin.example.com/public/api/v1/port-forward"), false);
  assert.equal(validWebSocketUrl("wss://admin.example.com/other"), false);
  assert.equal(
    validWebSocketUrl("wss://admin.example.com/public/api/v1/port-forward?ticket=secret"),
    false,
  );
});

test("verifies the exact release asset checksum", () => {
  const asset = "platformd-forward-linux-amd64";
  const binary = Buffer.from("release bytes");
  const checksum = createHash("sha256").update(binary).digest("hex");
  verifyChecksum(asset, binary, `${checksum}  ${asset}\n`);
  assert.throws(
    () => verifyChecksum(asset, Buffer.from("changed"), `${checksum}  ${asset}\n`),
    /checksum/,
  );
  assert.throws(() => verifyChecksum(asset, binary, `${checksum}  other\n`), /does not contain/);
});

test("downloads the selected release asset and makes it executable", async (t) => {
  const temporaryRoot = await mkdtemp(join(tmpdir(), "platformd-action-test-"));
  t.after(() => rm(temporaryRoot, { force: true, recursive: true }));
  const asset = "platformd-forward-linux-amd64";
  const binary = Buffer.from("release bytes");
  const checksum = createHash("sha256").update(binary).digest("hex");
  const requested: string[] = [];
  const downloaded = await downloadForward(
    { version: "1.2.3", target: { os: "linux", arch: "amd64" } },
    {
      temporaryRoot,
      fetchImplementation: async (url) => {
        requested.push(String(url));
        return String(url).endsWith("/SHA256SUMS")
          ? new Response(`${checksum}  ${asset}\n`)
          : new Response(binary);
      },
    },
  );

  assert.deepEqual(requested, [
    "https://github.com/iivankin/platformd/releases/download/v1.2.3/platformd-forward-linux-amd64",
    "https://github.com/iivankin/platformd/releases/download/v1.2.3/SHA256SUMS",
  ]);
  assert.deepEqual(await readFile(downloaded.binaryPath), binary);
  assert.equal((await stat(downloaded.binaryPath)).mode & 0o700, 0o700);
});

test("uses a local forward binary without downloading or verifying checksums", async (t) => {
  const temporaryRoot = await mkdtemp(join(tmpdir(), "platformd-action-test-"));
  t.after(() => rm(temporaryRoot, { force: true, recursive: true }));
  const binaryPath = join(temporaryRoot, "local-forward");
  await writeFile(binaryPath, "local binary", { mode: 0o700 });
  let fetched = false;
  const prepared = await prepareForwardBinary(
    {
      binaryPath,
      version: "1.2.3",
      target: { os: "linux", arch: "amd64" },
    },
    {
      temporaryRoot,
      fetchImplementation: async () => {
        fetched = true;
        throw new Error("should not download");
      },
    },
  );

  assert.equal(fetched, false);
  assert.equal(prepared.binaryPath, binaryPath);
  assert.match(prepared.workDir, /platformd-port-forward-/);
  assert.notEqual(prepared.workDir, temporaryRoot);
});

test("rejects a missing local forward binary", async () => {
  await assert.rejects(
    prepareForwardBinary({
      binaryPath: join(tmpdir(), "missing-platformd-forward"),
      version: "1.2.3",
      target: { os: "linux", arch: "amd64" },
    }),
    /forward binary not found/,
  );
});
