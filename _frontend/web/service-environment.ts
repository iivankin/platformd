import { newID } from "@/id";
import type { VariableRow } from "@/service-variable-model";

const environmentName = /^[A-Za-z_][A-Za-z0-9_]*$/u;
const exportPrefix = /^export\s+/u;

export const parseServiceEnvironment = (value: string) => {
  const environment: Record<string, string> = {};
  for (const rawLine of value.split(/\r?\n/u)) {
    let line = rawLine.trim();
    if (!line || line.startsWith("#")) {
      continue;
    }
    line = line.replace(exportPrefix, "");
    const separator = line.indexOf("=");
    const name = separator === -1 ? line : line.slice(0, separator).trim();
    if (separator === -1 || !environmentName.test(name)) {
      throw new Error(`Invalid environment line: ${rawLine}`);
    }
    environment[name] = line.slice(separator + 1);
  }
  return environment;
};

export const looksLikeServiceEnvironment = (value: string) => {
  try {
    const environment = parseServiceEnvironment(value);
    return Object.keys(environment).length > 0;
  } catch {
    return false;
  }
};

export const applyParsedEnvironment = (
  rows: VariableRow[],
  environment: Record<string, string>,
  preferRowID?: string
): VariableRow[] => {
  const entries = Object.entries(environment);
  if (entries.length === 0) {
    return rows;
  }

  const next = rows.map((row) => ({ ...row }));
  const claimed = new Set<string>();
  const additions: VariableRow[] = [];
  let preferIndex = preferRowID
    ? next.findIndex((row) => row.id === preferRowID)
    : -1;

  for (const [name, value] of entries) {
    const existingIndex = next.findIndex(
      (row) => !claimed.has(row.id) && row.name === name
    );
    if (existingIndex !== -1) {
      const existing = next[existingIndex];
      if (existing) {
        next[existingIndex] = { ...existing, name, value };
        claimed.add(existing.id);
        continue;
      }
    }

    const preferred = preferIndex === -1 ? undefined : next[preferIndex];
    if (
      preferred &&
      !claimed.has(preferred.id) &&
      (preferred.name === "" || preferred.name === name)
    ) {
      next[preferIndex] = { ...preferred, name, value };
      claimed.add(preferred.id);
      preferIndex = -1;
      continue;
    }

    const row = { id: newID(), name, value };
    additions.push(row);
    claimed.add(row.id);
  }

  return [...additions, ...next];
};
