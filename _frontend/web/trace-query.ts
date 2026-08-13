import type { ServiceTraceSummary } from "@/api";

type TraceQueryRecord = Pick<
  ServiceTraceSummary,
  "durationNano" | "errorSpanCount"
>;

const nanosToMilliseconds = (value: string) =>
  Number(globalThis.BigInt(value)) / 1_000_000;

const matchesDuration = (token: string, duration: number) => {
  const match =
    /^(?<operator>>=|<=|>|<)?(?<value>\d+(?:\.\d+)?)(?<unit>ms|s)$/u.exec(
      token
    );
  if (!match?.groups) {
    return false;
  }
  const threshold =
    Number(match.groups.value) * (match.groups.unit === "s" ? 1000 : 1);
  switch (match.groups.operator) {
    case ">": {
      return duration > threshold;
    }
    case ">=": {
      return duration >= threshold;
    }
    case "<": {
      return duration < threshold;
    }
    case "<=": {
      return duration <= threshold;
    }
    default: {
      return duration === threshold;
    }
  }
};

export const matchesTraceQuery = (trace: TraceQueryRecord, input: string) => {
  const tokens = input.trim().toLowerCase().split(/\s+/u).filter(Boolean);
  return tokens.every((token) => {
    const separator = token.indexOf(":");
    if (separator > 0) {
      const key = token.slice(0, separator);
      const value = token.slice(separator + 1);
      if (key === "status") {
        if (value === "error") {
          return trace.errorSpanCount > 0;
        }
        if (value === "ok") {
          return trace.errorSpanCount === 0;
        }
        return false;
      }
      if (key === "duration") {
        return matchesDuration(value, nanosToMilliseconds(trace.durationNano));
      }
    }
    return true;
  });
};

export const traceSearchText = (input: string) =>
  input
    .trim()
    .split(/\s+/u)
    .filter((token) => !/^(?:status|duration):/iu.test(token))
    .join(" ");
