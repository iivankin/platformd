import { expect, test } from "bun:test";

import { applyProjectOperations } from "@/project-apply";
import type { ProjectApplyOperation } from "@/project-apply";

const reference = (resource: string, output: string) =>
  `\${{${resource}.${output}}}`;

const operation = (
  resourceName: string,
  environment: Record<string, string>,
  run: () => Promise<unknown>
): ProjectApplyOperation => ({
  environment,
  id: resourceName,
  label: resourceName,
  resourceName,
  run,
});

test("orders variable references after pending resources", async () => {
  const started: string[] = [];
  const outcomes = await applyProjectOperations(
    [
      operation("database", {}, () => {
        started.push("database");
        return Promise.resolve();
      }),
      operation(
        "web",
        { DATABASE_URL: reference("database", "DATABASE_URL") },
        () => {
          started.push("web");
          return Promise.resolve();
        }
      ),
    ],
    new Set()
  );

  expect(started).toEqual(["database", "web"]);
  expect(outcomes.every((outcome) => outcome.status === "fulfilled")).toBe(
    true
  );
});

test("applies independent resources in parallel dependency waves", async () => {
  const started: string[] = [];
  const databaseGate = Promise.withResolvers<undefined>();
  const operations = [
    operation(
      "api",
      {
        DATABASE_URL: reference("database", "DATABASE_URL"),
        REDIS_URL: reference("cache", "REDIS_URL"),
      },
      () => {
        started.push("api");
        return Promise.resolve();
      }
    ),
    operation("cache", {}, () => {
      started.push("cache");
      return databaseGate.promise;
    }),
    operation("database", {}, () => {
      started.push("database");
      return databaseGate.promise;
    }),
    operation("worker", { API_URL: reference("api", "URL") }, () => {
      started.push("worker");
      return Promise.resolve();
    }),
    operation("independent", {}, () => {
      started.push("independent");
      return Promise.resolve();
    }),
  ];

  const applying = applyProjectOperations(operations, new Set());
  await Promise.resolve();
  expect(started).toEqual(["cache", "database", "independent"]);

  databaseGate.resolve();
  const outcomes = await applying;

  expect(started).toEqual([
    "cache",
    "database",
    "independent",
    "api",
    "worker",
  ]);
  expect(outcomes.every((outcome) => outcome.status === "fulfilled")).toBe(
    true
  );
});

test("blocks failed dependencies without stopping independent branches", async () => {
  const started: string[] = [];
  const outcomes = await applyProjectOperations(
    [
      operation("database", {}, () => {
        started.push("database");
        return Promise.reject(new Error("database failed"));
      }),
      operation(
        "api",
        { DATABASE_URL: reference("database", "DATABASE_URL") },
        () => {
          started.push("api");
          return Promise.resolve();
        }
      ),
      operation("independent", {}, () => {
        started.push("independent");
        return Promise.resolve();
      }),
    ],
    new Set()
  );

  expect(started).toEqual(["database", "independent"]);
  expect(
    outcomes.map(({ operation: applied, status }) => [
      applied.resourceName,
      status,
    ])
  ).toEqual([
    ["database", "rejected"],
    ["independent", "fulfilled"],
    ["api", "blocked"],
  ]);
});

test("rejects dependency cycles before applying anything", async () => {
  let started = false;
  const run = () => {
    started = true;
    return Promise.resolve();
  };

  await expect(
    applyProjectOperations(
      [
        operation("api", { WORKER_URL: reference("worker", "URL") }, run),
        operation("worker", { API_URL: reference("api", "URL") }, run),
      ],
      new Set()
    )
  ).rejects.toThrow("Variable reference cycle");
  expect(started).toBe(false);
});

test("rejects a missing referenced resource before applying anything", async () => {
  let started = false;

  await expect(
    applyProjectOperations(
      [
        operation(
          "api",
          { DATABASE_URL: reference("missing", "DATABASE_URL") },
          () => {
            started = true;
            return Promise.resolve();
          }
        ),
      ],
      new Set(["existing"])
    )
  ).rejects.toThrow("api references missing resource missing");
  expect(started).toBe(false);
});
