import assert from "node:assert/strict";
import test from "node:test";
import { normalizeBaseUrl, normalizeVersion, readConfig, resolveTarget } from "./config.js";

function inputs(overrides: Record<string, string> = {}): (name: string) => string {
  const values: Record<string, string> = {
    url: "https://admin.example.com",
    token: "secret",
    project: "shop",
    resource: "backend",
    endpoint: "",
    port: "8080",
    "local-port": "",
    "expires-in-seconds": "900",
    "platformd-version": "1.2.3",
    "binary-path": "",
    "connection-url": "",
    "connection-env": "",
    ...overrides,
  };
  return (name) => values[name] || "";
}

test("reads a complete service tunnel configuration", () => {
  assert.deepEqual(readConfig(inputs(), "linux", "x64"), {
    baseUrl: "https://admin.example.com",
    token: "secret",
    project: "shop",
    resource: "backend",
    endpoint: "",
    port: 8080,
    localPort: 8080,
    expiresInSeconds: 900,
    version: "1.2.3",
    binaryPath: "",
    connectionUrl: "",
    connectionEnv: "",
    target: { os: "linux", arch: "amd64" },
  });
});

test("uses the fixed port for a service errors endpoint", () => {
  const config = readConfig(inputs({ endpoint: "errors", port: "" }));
  assert.equal(config.endpoint, "errors");
  assert.equal(config.port, 9001);
  assert.equal(config.localPort, 9001);
  assert.throws(
    () => readConfig(inputs({ endpoint: "errors", port: "9001" })),
    /port must be omitted/,
  );
});

test("accepts a local forward binary path", () => {
  const config = readConfig(inputs({ "binary-path": "./bin/platformd-forward" }));
  assert.equal(config.binaryPath, "./bin/platformd-forward");
});

test("accepts an explicit local port and latest helper", () => {
  const config = readConfig(
    inputs({ "local-port": "18080", "platformd-version": "latest" }),
    "darwin",
    "arm64",
  );
  assert.equal(config.localPort, 18080);
  assert.equal(config.version, "latest");
  assert.deepEqual(config.target, { os: "darwin", arch: "arm64" });
});

test("rejects unsafe API URLs", () => {
  for (const value of [
    "http://admin.example.com",
    "https://user:password@admin.example.com",
    "https://admin.example.com/base",
    "not a URL",
  ]) {
    assert.throws(() => normalizeBaseUrl(value), /valid HTTPS origin/);
  }
});

test("rejects invalid ranges", () => {
  assert.throws(() => readConfig(inputs({ port: "0" })), /port must be an integer/);
  assert.throws(
    () => readConfig(inputs({ "expires-in-seconds": "28801" })),
    /expires-in-seconds must be an integer/,
  );
});

test("normalizes stable versions and rejects mutable or prerelease tags", () => {
  assert.equal(normalizeVersion("v1.2.3"), "1.2.3");
  assert.equal(normalizeVersion("latest"), "latest");
  assert.throws(() => normalizeVersion("main"), /exact stable SemVer/);
  assert.throws(() => normalizeVersion("1.2.3-beta.1"), /exact stable SemVer/);
});

test("rejects unsupported runner targets", () => {
  assert.throws(() => resolveTarget("win32", "x64"), /unsupported runner platform/);
  assert.throws(() => resolveTarget("linux", "ia32"), /unsupported runner platform/);
});

test("accepts connection URL options before platformd detects the kind", () => {
  const config = readConfig(
    inputs({
      "connection-url": "rediss://default:secret@redis:6379",
      "connection-env": "CACHE_URL",
    }),
  );
  assert.equal(config.connectionEnv, "CACHE_URL");
});

test("rejects connection export inputs that cannot be applied safely", () => {
  assert.throws(
    () => readConfig(inputs({ "connection-env": "POSTGRES_URL" })),
    /connection-env requires connection-url/,
  );
  assert.throws(
    () =>
      readConfig(
        inputs({
          "connection-url": "postgres://database/app",
          "connection-env": "INVALID-NAME",
        }),
      ),
    /valid environment variable name/,
  );
});
