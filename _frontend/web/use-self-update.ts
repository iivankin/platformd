import { useEffect, useState } from "react";

import { APIError, applySelfUpdate, fetchMeta } from "@/api";

const restartTimeout = 10 * 60 * 1000;

export const useSelfUpdate = (refreshStatus?: () => Promise<void>) => {
  const [updateError, setUpdateError] = useState<string>();
  const [updating, setUpdating] = useState(false);
  const [targetVersion, setTargetVersion] = useState<string>();

  useEffect(() => {
    if (!targetVersion) {
      return;
    }
    const controller = new AbortController();
    const deadline = Date.now() + restartTimeout;
    let timeout: number | undefined;
    const probe = async () => {
      try {
        const meta = await fetchMeta(controller.signal);
        if (meta.version === targetVersion) {
          window.location.reload();
          return;
        }
      } catch (probeError) {
        if (
          probeError instanceof DOMException &&
          probeError.name === "AbortError"
        ) {
          return;
        }
      }
      if (Date.now() >= deadline) {
        setUpdating(false);
        setUpdateError(
          "The new control plane did not become ready within 10 minutes. Use local recovery commands on the VPS."
        );
        return;
      }
      timeout = window.setTimeout(() => void probe(), 2000);
    };
    timeout = window.setTimeout(() => void probe(), 2000);
    return () => {
      controller.abort();
      if (timeout !== undefined) {
        window.clearTimeout(timeout);
      }
    };
  }, [targetVersion]);

  const start = async () => {
    setUpdating(true);
    setUpdateError(undefined);
    try {
      const result = await applySelfUpdate();
      setTargetVersion(result.targetVersion);
    } catch (error) {
      setUpdating(false);
      if (error instanceof APIError && error.code === "already_up_to_date") {
        await refreshStatus?.();
        return;
      }
      setUpdateError(
        error instanceof Error
          ? error.message
          : "Unable to start platform update"
      );
    }
  };

  return { error: updateError, start, targetVersion, updating };
};
