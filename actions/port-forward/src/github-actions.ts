import { appendFileSync } from "node:fs";
import { randomUUID } from "node:crypto";

export function getInput(name: string): string {
  return (process.env[`INPUT_${name.replace(/ /g, "_").toUpperCase()}`] || "").trim();
}

export function getState(name: string): string {
  return process.env[`STATE_${name}`] || "";
}

export function exportVariable(name: string, value: string): void {
  process.env[name] = String(value);
  writeValue("GITHUB_ENV", "set-env", name, value);
}

export function saveState(name: string, value: string): void {
  writeValue("GITHUB_STATE", "save-state", name, value);
}

export function setOutput(name: string, value: string): void {
  writeValue("GITHUB_OUTPUT", "set-output", name, value);
}

export function setSecret(value: string): void {
  issue("add-mask", value);
}

export function setFailed(message: string): void {
  process.exitCode = 1;
  issue("error", message);
}

export function info(message: string): void {
  console.log(message);
}

export function warning(message: string): void {
  issue("warning", message);
}

function writeValue(
  fileVariable: string,
  fallbackCommand: string,
  name: string,
  value: string,
): void {
  const file = process.env[fileVariable];
  if (!file) {
    issue(fallbackCommand, value, { name });
    return;
  }
  const delimiter = `platformd_${randomUUID()}`;
  const text = String(value);
  if (name.includes(delimiter) || text.includes(delimiter)) {
    throw new Error(`unable to write ${name} to ${fileVariable}`);
  }
  appendFileSync(file, `${name}<<${delimiter}\n${text}\n${delimiter}\n`, "utf8");
}

function issue(
  command: string,
  message: string,
  properties: Record<string, string> = {},
): void {
  const fields = Object.entries(properties)
    .map(([name, value]) => `${name}=${escapeProperty(value)}`)
    .join(",");
  console.log(`::${command}${fields ? ` ${fields}` : ""}::${escapeData(message)}`);
}

function escapeData(value: string): string {
  return String(value).replaceAll("%", "%25").replaceAll("\r", "%0D").replaceAll("\n", "%0A");
}

function escapeProperty(value: string): string {
  return escapeData(value).replaceAll(":", "%3A").replaceAll(",", "%2C");
}
