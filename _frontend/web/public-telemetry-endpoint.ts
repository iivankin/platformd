import type { ServiceDomain } from "@/api";

export const disabledTelemetryEndpoint = "__disabled__";
export const dedicatedTelemetryEndpoint = "__dedicated__";

const validASCIIPathCharacter = /^[A-Za-z0-9\-._~!$&'()*+,;=:@/]$/u;

const hasInvalidPathCharacter = (value: string) =>
  [...value].some((character) => {
    const code = character.codePointAt(0) ?? 0;
    return code <= 0x7f && !validASCIIPathCharacter.test(character);
  });

export const validTelemetryPublicPath = (value: string) => {
  const path = value.trim();
  if (path === "") {
    return true;
  }
  if (
    new TextEncoder().encode(path).byteLength > 256 ||
    path === "/" ||
    !path.startsWith("/") ||
    path.endsWith("/") ||
    path.includes("//") ||
    hasInvalidPathCharacter(path)
  ) {
    return false;
  }
  return !path
    .split("/")
    .some((segment) => segment === "." || segment === "..");
};

export const telemetryEndpointSelection = (
  hostname: string | undefined,
  domains: ServiceDomain[]
) => {
  if (!hostname) {
    return disabledTelemetryEndpoint;
  }
  if (domains.some((domain) => domain.hostname === hostname)) {
    return hostname;
  }
  return dedicatedTelemetryEndpoint;
};
