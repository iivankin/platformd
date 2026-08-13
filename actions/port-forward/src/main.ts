import * as core from "./github-actions.js";
import { tmpdir } from "node:os";
import { readConfig } from "./config.js";
import { forwardedConnection } from "./connection-url.js";
import { createPortForward, prepareForwardBinary } from "./platformd.js";
import { removeWorkDir, startForward, stopForward, waitForReady } from "./forward-process.js";

export async function run(): Promise<void> {
  let pid: number | undefined;
  let workDir: string | undefined;
  try {
    const config = readConfig((name) => core.getInput(name));
    if (config.token) {
      core.setSecret(config.token);
    }
    if (config.connectionUrl) {
      core.setSecret(config.connectionUrl);
    }

    if (config.binaryPath) {
      core.info(`Using local platformd-forward at ${config.binaryPath}`);
    } else {
      core.info(
        `Downloading platformd-forward ${config.version} for ${config.target.os}/${config.target.arch}`,
      );
    }
    const prepared = await prepareForwardBinary(config);
    workDir = prepared.workDir;
    core.saveState("work-dir", workDir);

    if (config.token) {
      core.info("Creating port forward with API token");
    } else {
      core.info("Creating port forward with GitHub Actions OIDC");
    }
    const grant = await createPortForward(config);
    core.setSecret(grant.ticket);
    const connection = forwardedConnection({ ...config, resourceKind: grant.resourceKind });
    if (connection) {
      core.setSecret(connection.url);
    }

    const forward = await startForward({
      binaryPath: prepared.binaryPath,
      websocketUrl: grant.websocketUrl,
      ticket: grant.ticket,
      localPort: config.localPort,
      httpHost: grant.endpointHost,
      workDir,
    });
    pid = forward.child.pid ?? undefined;
    if (pid === undefined) {
      throw new Error("platformd-forward did not start");
    }
    core.saveState("forward-pid", String(pid));

    await waitForReady(forward.child, forward.logPath, config.localPort);
    core.setOutput("host", "127.0.0.1");
    core.setOutput("port", String(config.localPort));
    core.setOutput("expires-at", grant.expiresAt);
    if (grant.endpointHost) {
      const sentryUrl = `http://127.0.0.1:${config.localPort}`;
      core.setOutput("sentry-url", sentryUrl);
      core.info(`Sentry endpoint available at ${sentryUrl}`);
    }
    if (connection) {
      core.exportVariable(connection.environment, connection.url);
      core.info(`Exported tunneled connection as ${connection.environment}`);
    }
    core.info(
      `Forwarding 127.0.0.1:${config.localPort} to ${grant.resourceKind} ${config.project}/${config.resource}`,
    );
  } catch (error) {
    if (pid) {
      await stopForward(pid).catch(() => {});
    }
    if (workDir) {
      const temporaryRoot = process.env.RUNNER_TEMP || tmpdir();
      await removeWorkDir(workDir, temporaryRoot).catch(() => {});
    }
    core.setFailed(error instanceof Error ? error.message : String(error));
  }
}
