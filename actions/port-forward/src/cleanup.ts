import * as core from "./github-actions.js";
import { tmpdir } from "node:os";
import { removeWorkDir, stopForward } from "./forward-process.js";

export async function run(): Promise<void> {
  const pidValue = core.getState("forward-pid");
  const workDir = core.getState("work-dir");
  try {
    if (pidValue) {
      await stopForward(Number(pidValue));
    }
    if (workDir) {
      await removeWorkDir(workDir, process.env.RUNNER_TEMP || tmpdir());
    }
  } catch (error) {
    core.warning(
      `Unable to clean up platformd-forward: ${error instanceof Error ? error.message : String(error)}`,
    );
  }
}
