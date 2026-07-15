import type { ResourceUsage } from "@/api";

export const cpuMillicoresBetween = (
  previous: ResourceUsage,
  current: ResourceUsage
): number | undefined => {
  const elapsedMillis = current.observedAt - previous.observedAt;
  const reset = current.cpuUsageMicros < previous.cpuUsageMicros;
  if (elapsedMillis <= 0 || reset || !(previous.running && current.running)) {
    return;
  }
  return Math.round(
    (current.cpuUsageMicros - previous.cpuUsageMicros) / elapsedMillis
  );
};

export const networkBytesPerSecondBetween = (
  previous: ResourceUsage,
  current: ResourceUsage
): { egress: number; ingress: number } | undefined => {
  const elapsedMillis = current.observedAt - previous.observedAt;
  const reset =
    current.networkRxBytes < previous.networkRxBytes ||
    current.networkTxBytes < previous.networkTxBytes;
  if (
    elapsedMillis <= 0 ||
    reset ||
    !(previous.running && current.running) ||
    !(previous.networkAvailable && current.networkAvailable)
  ) {
    return;
  }
  return {
    egress: Math.round(
      ((current.networkTxBytes - previous.networkTxBytes) * 1000) /
        elapsedMillis
    ),
    ingress: Math.round(
      ((current.networkRxBytes - previous.networkRxBytes) * 1000) /
        elapsedMillis
    ),
  };
};
