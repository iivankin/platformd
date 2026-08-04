import assert from "node:assert/strict";
import test from "node:test";
import { forwardedConnection } from "./connection-url.js";

test("rewrites PostgreSQL host and port while preserving credentials and database", () => {
  assert.deepEqual(
    forwardedConnection({
      resourceKind: "postgres",
      localPort: 15432,
      connectionEnv: "POSTGRES_URL",
      connectionUrl: "postgresql://app:p%40ss@database.internal:5432/app?sslmode=require",
    }),
    {
      environment: "POSTGRES_URL",
      url: "postgresql://app:p%40ss@127.0.0.1:15432/app?sslmode=require",
    },
  );
});

test("preserves Redis TLS and selects the configured environment", () => {
  assert.deepEqual(
    forwardedConnection({
      resourceKind: "redis",
      localPort: 16379,
      connectionEnv: "CACHE_URL",
      connectionUrl: "rediss://default:secret@redis.internal:6379/2",
    }),
    {
      environment: "CACHE_URL",
      url: "rediss://default:secret@127.0.0.1:16379/2",
    },
  );
});

test("selects the standard environment from the detected resource kind", () => {
  assert.equal(
    forwardedConnection({
      resourceKind: "postgres",
      localPort: 15432,
      connectionEnv: "",
      connectionUrl: "postgres://database/app",
    })?.environment,
    "POSTGRES_URL",
  );
  assert.equal(
    forwardedConnection({
      resourceKind: "redis",
      localPort: 16379,
      connectionEnv: "",
      connectionUrl: "redis://cache/0",
    })?.environment,
    "REDIS_URL",
  );
});

test("rejects a connection URL for the wrong resource protocol", () => {
  assert.throws(
    () =>
      forwardedConnection({
        resourceKind: "postgres",
        localPort: 15432,
        connectionEnv: "POSTGRES_URL",
        connectionUrl: "redis://database:6379",
      }),
    /must use postgres/,
  );
});

test("rejects connection URL export for a detected service", () => {
  assert.throws(
    () =>
      forwardedConnection({
        resourceKind: "service",
        localPort: 18080,
        connectionEnv: "SERVICE_URL",
        connectionUrl: "tcp://service:8080",
      }),
    /supported only for postgres and redis/,
  );
});
