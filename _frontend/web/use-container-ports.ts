import { useEffect, useState } from "react";

import { fetchContainerPorts } from "@/api";
import type { ContainerPort } from "@/api";

export type ContainerPortDetectionStatus = "loading" | "ready" | "unavailable";

export const useContainerPorts = (
  projectID: string,
  resourceKind: "postgres" | "redis" | "service",
  resourceID: string
) => {
  const [ports, setPorts] = useState<ContainerPort[]>([]);
  const [status, setStatus] = useState<ContainerPortDetectionStatus>("loading");

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      setStatus("loading");
      try {
        const detected = await fetchContainerPorts(
          projectID,
          resourceKind,
          resourceID,
          controller.signal
        );
        if (!controller.signal.aborted) {
          setPorts(detected);
          setStatus("ready");
        }
      } catch (error) {
        if (!(error instanceof DOMException && error.name === "AbortError")) {
          setPorts([]);
          setStatus("unavailable");
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, resourceID, resourceKind]);

  return { ports, status };
};
