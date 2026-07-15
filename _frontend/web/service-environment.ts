const environmentName = /^[A-Za-z_][A-Za-z0-9_]*$/u;

export const parseServiceEnvironment = (value: string) => {
  const environment: Record<string, string> = {};
  for (const rawLine of value.split("\n")) {
    const line = rawLine.trim();
    if (!line) {
      continue;
    }
    const separator = line.indexOf("=");
    const name = separator === -1 ? line : line.slice(0, separator).trim();
    if (separator === -1 || !environmentName.test(name)) {
      throw new Error(`Invalid environment line: ${rawLine}`);
    }
    environment[name] = line.slice(separator + 1);
  }
  return environment;
};

export const formatServiceEnvironment = (environment: Record<string, string>) =>
  Object.entries(environment)
    .toSorted(([left], [right]) => left.localeCompare(right))
    .map(([name, value]) => `${name}=${value}`)
    .join("\n");
