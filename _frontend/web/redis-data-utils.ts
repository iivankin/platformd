export type RedisInputEncoding = "base64url" | "text";

export const textToBase64URL = (value: string): string => {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCodePoint(byte);
  }
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/u, "");
};

export const redisInputBytes = (
  value: string,
  encoding: RedisInputEncoding
): string => {
  if (encoding === "text") {
    return textToBase64URL(value);
  }
  if (!/^[\w-]*$/u.test(value)) {
    throw new Error("Binary value must be unpadded base64url");
  }
  return value;
};

export const redisDisplayValue = (value: { base64: string; text?: string }) =>
  value.text ?? `base64:${value.base64}`;

export const formatBytes = (bytes: number) => {
  if (bytes < 1024) {
    return `${bytes} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KiB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
};

export const formatTTL = (milliseconds?: number) => {
  if (milliseconds === undefined) {
    return "Persistent";
  }
  if (milliseconds < 60_000) {
    return `${Math.ceil(milliseconds / 1000)}s`;
  }
  if (milliseconds < 3_600_000) {
    return `${Math.ceil(milliseconds / 60_000)}m`;
  }
  return `${Math.ceil(milliseconds / 3_600_000)}h`;
};
