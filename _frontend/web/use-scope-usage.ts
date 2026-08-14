import { useEffect, useState } from "react";

import {
  fetchHostUsageHistory,
  fetchInstallationUsage,
  fetchInstallationUsageHistory,
  fetchProjectUsage,
  fetchProjectUsageHistory,
} from "@/api";
import type {
  ResourceUsage,
  ResourceUsageHistory,
  ResourceUsageRange,
} from "@/api";

const liveRefreshMillis = 2000;
const historyRefreshMillis = 60_000;

type UsageScope = { kind: "installation" } | { id: string; kind: "project" };

const messageFor = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const isAbort = (error: unknown) =>
  error instanceof DOMException && error.name === "AbortError";

export const useScopeUsage = (scope: UsageScope, range: ResourceUsageRange) => {
  const [usage, setUsage] = useState<ResourceUsage | null>(null);
  const [history, setHistory] = useState<ResourceUsageHistory | null>(null);
  const [hostHistory, setHostHistory] = useState<ResourceUsageHistory | null>(
    null
  );
  const [currentError, setCurrentError] = useState<string>();
  const [historyError, setHistoryError] = useState<string>();
  const [hostHistoryError, setHostHistoryError] = useState<string>();
  const [historyLoading, setHistoryLoading] = useState(true);
  const projectID = scope.kind === "project" ? scope.id : undefined;

  useEffect(() => {
    const controller = new AbortController();
    let inFlight = false;
    const load = async () => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      try {
        const current = projectID
          ? await fetchProjectUsage(projectID, controller.signal)
          : await fetchInstallationUsage(controller.signal);
        setUsage(current);
        setCurrentError(undefined);
      } catch (error) {
        if (!isAbort(error)) {
          setCurrentError(messageFor(error, "Unable to read current usage"));
        }
      } finally {
        inFlight = false;
      }
    };
    void load();
    const interval = globalThis.setInterval(
      () => void load(),
      liveRefreshMillis
    );
    return () => {
      controller.abort();
      globalThis.clearInterval(interval);
    };
  }, [projectID, scope.kind]);

  useEffect(() => {
    const controller = new AbortController();
    let inFlight = false;
    const load = async () => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      setHistoryLoading(true);
      try {
        const [nextHistory, nextHostHistory] = projectID
          ? [
              await fetchProjectUsageHistory(
                projectID,
                range,
                controller.signal
              ),
              null,
            ]
          : await Promise.all([
              fetchInstallationUsageHistory(range, controller.signal),
              fetchHostUsageHistory(range, controller.signal),
            ]);
        setHistory(nextHistory);
        setHostHistory(nextHostHistory);
        setHistoryError(undefined);
        setHostHistoryError(undefined);
      } catch (error) {
        if (!isAbort(error)) {
          setHistoryError(messageFor(error, "Unable to read usage history"));
          if (!projectID) {
            setHostHistoryError(
              messageFor(error, "Unable to read VPS usage history")
            );
          }
        }
      } finally {
        inFlight = false;
        if (!controller.signal.aborted) {
          setHistoryLoading(false);
        }
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
  }, [projectID, range, scope.kind]);

  const network =
    usage?.networkIngressBytesPerSecond !== undefined &&
    usage.networkEgressBytesPerSecond !== undefined
      ? {
          egress: usage.networkEgressBytesPerSecond,
          ingress: usage.networkIngressBytesPerSecond,
        }
      : undefined;

  return {
    cpuMillicores: usage?.cpuMillicores,
    currentError,
    history,
    historyError,
    historyLoading,
    hostHistory,
    hostHistoryError,
    network,
    usage,
  };
};
