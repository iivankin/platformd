import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { exportVariable, getInput, getState, saveState, setOutput } from "./github-actions.js";

test("reads action inputs and post-action state", () => {
  process.env.INPUT_PROJECT = " project ";
  process.env["STATE_forward-pid"] = "123";
  assert.equal(getInput("project"), "project");
  assert.equal(getState("forward-pid"), "123");
  delete process.env.INPUT_PROJECT;
  delete process.env["STATE_forward-pid"];
});

test("writes outputs and state through GitHub command files", async (t) => {
  const directory = mkdtempSync(join(tmpdir(), "platformd-action-command-"));
  t.after(() => rm(directory, { force: true, recursive: true }));
  const outputFile = join(directory, "output");
  const stateFile = join(directory, "state");
  const environmentFile = join(directory, "environment");
  writeFileSync(outputFile, "");
  writeFileSync(stateFile, "");
  writeFileSync(environmentFile, "");
  process.env.GITHUB_OUTPUT = outputFile;
  process.env.GITHUB_STATE = stateFile;
  process.env.GITHUB_ENV = environmentFile;
  t.after(() => {
    delete process.env.GITHUB_OUTPUT;
    delete process.env.GITHUB_STATE;
    delete process.env.GITHUB_ENV;
    delete process.env.POSTGRES_URL;
  });

  setOutput("port", "15432");
  saveState("forward-pid", "123");
  exportVariable("POSTGRES_URL", "postgres://127.0.0.1:15432/app");

  assert.match(readFileSync(outputFile, "utf8"), /^port<<platformd_[^\n]+\n15432\nplatformd_[^\n]+\n$/);
  assert.match(
    readFileSync(stateFile, "utf8"),
    /^forward-pid<<platformd_[^\n]+\n123\nplatformd_[^\n]+\n$/,
  );
  assert.match(
    readFileSync(environmentFile, "utf8"),
    /^POSTGRES_URL<<platformd_[^\n]+\npostgres:\/\/127\.0\.0\.1:15432\/app\nplatformd_[^\n]+\n$/,
  );
  assert.equal(process.env.POSTGRES_URL, "postgres://127.0.0.1:15432/app");
});
