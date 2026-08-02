import { useEffect, useState } from "react";

import { fetchResourceUsage, fetchResourceUsageHistory } from "@/api";
import type {
  ResourceUsage,
  ResourceUsageHistory,
  ResourceUsageKind,
  ResourceUsageRange,
} from "@/api";

const currentRefreshMillis = 2000;
const historyRefreshMillis = 60_000;

const messageFor = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const isAbort = (error: unknown) =>
  error instanceof DOMException && error.name === "AbortError";

export const useCurrentResourceUsage = (
  kind: ResourceUsageKind,
  resourceID: string
) => {
  const [usage, setUsage] = useState<ResourceUsage | null>(null);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    let inFlight = false;
    const load = async () => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      try {
        const current = await fetchResourceUsage(
          kind,
          resourceID,
          controller.signal
        );
        setUsage(current);
        setError(undefined);
      } catch (loadError) {
        if (!isAbort(loadError)) {
          setError(messageFor(loadError, "Unable to read resource usage"));
        }
      } finally {
        inFlight = false;
      }
    };
    void load();
    const interval = globalThis.setInterval(
      () => void load(),
      currentRefreshMillis
    );
    return () => {
      controller.abort();
      globalThis.clearInterval(interval);
    };
  }, [kind, resourceID]);

  const network =
    usage?.networkIngressBytesPerSecond !== undefined &&
    usage.networkEgressBytesPerSecond !== undefined
      ? {
          egress: usage.networkEgressBytesPerSecond,
          ingress: usage.networkIngressBytesPerSecond,
        }
      : undefined;

  return { cpuMillicores: usage?.cpuMillicores, error, network, usage };
};

export const useResourceUsageHistory = (
  kind: ResourceUsageKind,
  resourceID: string,
  range: ResourceUsageRange
) => {
  const [history, setHistory] = useState<ResourceUsageHistory | null>(null);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    let inFlight = false;
    const load = async () => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      try {
        const current = await fetchResourceUsageHistory(
          kind,
          resourceID,
          range,
          controller.signal
        );
        setHistory(current);
        setError(undefined);
      } catch (loadError) {
        if (!isAbort(loadError)) {
          setError(messageFor(loadError, "Unable to read resource history"));
        }
      } finally {
        inFlight = false;
      }
    };
    void load();
    const interval = globalThis.setInterval(
      () => void load(),
      historyRefreshMillis
    );
    return () => {
      controller.abort();
      globalThis.clearInterval(interval);
    };
  }, [kind, range, resourceID]);

  return { error, history };
};
