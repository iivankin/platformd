const PREVIEW_CHARS = 180;

export function collapsePreview(text: string, maxChars = PREVIEW_CHARS): string {
  const collapsed = text.replace(/\s+/gu, " ").trim();
  if (collapsed.length <= maxChars) {
    return collapsed;
  }
  return `${collapsed.slice(0, maxChars)}…`;
}

export function formatError(error: unknown): string {
  const parts: string[] = [];
  const seen = new Set<unknown>();
  const walk = (value: unknown): void => {
    if (value === undefined || value === null) {
      return;
    }
    if (typeof value === "object") {
      if (seen.has(value)) {
        return;
      }
      seen.add(value);
    }
    if (value instanceof AggregateError) {
      parts.push(value.message || "AggregateError");
      for (const inner of value.errors) {
        walk(inner);
      }
      return;
    }
    if (value instanceof Error) {
      parts.push(errorDetail(value));
      walk(value.cause);
      return;
    }
    parts.push(String(value));
  };
  walk(error);
  return parts.join(": ");
}

function errorDetail(error: Error): string {
  const extras: string[] = [];
  const record = error as Error & {
    code?: unknown;
    syscall?: unknown;
    errno?: unknown;
    address?: unknown;
    port?: unknown;
  };
  if (typeof record.code === "string" && record.code) {
    extras.push(`code=${record.code}`);
  }
  if (typeof record.syscall === "string" && record.syscall) {
    extras.push(`syscall=${record.syscall}`);
  }
  if (typeof record.errno === "number" || typeof record.errno === "string") {
    extras.push(`errno=${String(record.errno)}`);
  }
  if (typeof record.address === "string" && record.address) {
    extras.push(`address=${record.address}`);
  }
  if (typeof record.port === "number" || typeof record.port === "string") {
    extras.push(`port=${String(record.port)}`);
  }
  const message = error.message || error.name;
  return extras.length > 0 ? `${message} (${extras.join(" ")})` : message;
}
