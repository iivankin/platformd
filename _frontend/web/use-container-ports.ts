import { useEffect, useState } from "react";

import { fetchContainerPorts } from "@/api";
import type { ContainerPort } from "@/api";

export type ContainerPortDetectionStatus = "loading" | "ready" | "unavailable";

export const useContainerPorts = (
  projectID: string,
  resourceKind: "postgres" | "redis" | "service",
  resourceID: string,
  enabled = true
) => {
  const requestKey =
    enabled && projectID && resourceID
      ? `${projectID}:${resourceKind}:${resourceID}`
      : "";
  const [result, setResult] = useState<{
    key: string;
    ports: ContainerPort[];
    status: Exclude<ContainerPortDetectionStatus, "loading">;
  }>();

  useEffect(() => {
    if (!requestKey) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const detected = await fetchContainerPorts(
          projectID,
          resourceKind,
          resourceID,
          controller.signal
        );
        if (!controller.signal.aborted) {
          setResult({ key: requestKey, ports: detected, status: "ready" });
        }
      } catch (error) {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          setResult({ key: requestKey, ports: [], status: "unavailable" });
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, requestKey, resourceID, resourceKind]);

  if (!requestKey) {
    return { ports: [], status: "ready" as const };
  }
  if (result?.key !== requestKey) {
    return { ports: [], status: "loading" as const };
  }
  return { ports: result.ports, status: result.status };
};
