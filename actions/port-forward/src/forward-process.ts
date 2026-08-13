import { closeSync, openSync, readFileSync } from "node:fs";
import { rm } from "node:fs/promises";
import { basename, join, relative, resolve } from "node:path";
import { type ChildProcess, spawn } from "node:child_process";

export type StartForwardOptions = {
  binaryPath: string;
  websocketUrl: string;
  ticket: string;
  localPort: number;
  httpHost: string;
  workDir: string;
};

export type StartedForward = {
  child: ChildProcess;
  logPath: string;
};

export async function startForward({
  binaryPath,
  websocketUrl,
  ticket,
  localPort,
  httpHost,
  workDir,
}: StartForwardOptions): Promise<StartedForward> {
  const logPath = join(workDir, "platformd-forward.log");
  const logDescriptor = openSync(logPath, "a", 0o600);
  let child: ChildProcess;
  try {
    const commandArguments = ["--url", websocketUrl, "--local-port", String(localPort)];
    if (httpHost) {
      commandArguments.push("--http-host", httpHost);
    }
    child = spawn(binaryPath, commandArguments, {
      cwd: workDir,
      detached: true,
      env: { ...process.env, PLATFORMD_FORWARD_TICKET: ticket },
      stdio: ["ignore", logDescriptor, logDescriptor],
    });
    await new Promise<void>((resolveSpawn, rejectSpawn) => {
      child.once("spawn", () => resolveSpawn());
      child.once("error", rejectSpawn);
    });
  } finally {
    closeSync(logDescriptor);
  }
  child.unref();
  return { child, logPath };
}

export async function waitForReady(
  child: ChildProcess,
  logPath: string,
  localPort: number,
  timeoutMilliseconds = 10000,
): Promise<void> {
  const readyLine = `Forwarding 127.0.0.1:${localPort} to the platformd resource`;
  const deadline = Date.now() + timeoutMilliseconds;
  while (Date.now() < deadline) {
    const log = readLog(logPath);
    if (log.includes(readyLine)) {
      return;
    }
    if (child.exitCode !== null) {
      throw new Error(`platformd-forward exited before becoming ready${logSuffix(log)}`);
    }
    await delay(100);
  }
  throw new Error(
    `platformd-forward did not become ready within 10 seconds${logSuffix(readLog(logPath))}`,
  );
}

export async function stopForward(pid: number, timeoutMilliseconds = 3000): Promise<void> {
  if (!Number.isSafeInteger(pid) || pid < 2 || !isAlive(pid)) {
    return;
  }
  signalProcessGroup(pid, "SIGTERM");
  const deadline = Date.now() + timeoutMilliseconds;
  while (Date.now() < deadline) {
    if (!isAlive(pid)) {
      return;
    }
    await delay(100);
  }
  signalProcessGroup(pid, "SIGKILL");
}

export async function removeWorkDir(workDir: string, temporaryRoot: string): Promise<void> {
  if (!workDir) {
    return;
  }
  const resolvedDirectory = resolve(workDir);
  const resolvedRoot = resolve(temporaryRoot);
  const pathFromRoot = relative(resolvedRoot, resolvedDirectory);
  if (
    !pathFromRoot ||
    pathFromRoot.startsWith("..") ||
    basename(resolvedDirectory).startsWith("platformd-port-forward-") === false
  ) {
    throw new Error("refusing to remove an unexpected action work directory");
  }
  await rm(resolvedDirectory, { force: true, recursive: true });
}

function isAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch (error) {
    if (error && typeof error === "object" && "code" in error && error.code === "EPERM") {
      return true;
    }
    return false;
  }
}

function signalProcessGroup(pid: number, signal: NodeJS.Signals): void {
  try {
    process.kill(-pid, signal);
  } catch (error) {
    if (!(error && typeof error === "object" && "code" in error && error.code === "ESRCH")) {
      throw error;
    }
  }
}

function readLog(logPath: string): string {
  try {
    return readFileSync(logPath, "utf8").trim();
  } catch (error) {
    if (error && typeof error === "object" && "code" in error && error.code === "ENOENT") {
      return "";
    }
    throw error;
  }
}

function logSuffix(log: string): string {
  return log ? `: ${log}` : "";
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}
