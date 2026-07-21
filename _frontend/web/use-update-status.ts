import { useCallback, useEffect, useRef, useState } from "react";

import { fetchSelfUpdateStatus } from "@/api";
import type { SelfUpdateStatus } from "@/api";

const refreshInterval = 4 * 60 * 60 * 1000;

export const useUpdateStatus = (enabled: boolean) => {
  const [status, setStatus] = useState<SelfUpdateStatus>();
  const [error, setError] = useState<string>();
  const [checking, setChecking] = useState(false);
  const [checkedAt, setCheckedAt] = useState<number>();
  const request = useRef<AbortController | null>(null);

  const refresh = useCallback(async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setChecking(true);
    setError(undefined);
    try {
      setStatus(await fetchSelfUpdateStatus(controller.signal));
      setCheckedAt(Date.now());
    } catch (checkError) {
      if (
        !(
          checkError instanceof DOMException && checkError.name === "AbortError"
        )
      ) {
        setError(
          checkError instanceof Error
            ? checkError.message
            : "Unable to check for platform updates"
        );
      }
    } finally {
      if (request.current === controller) {
        request.current = null;
        setChecking(false);
      }
    }
  }, []);

  useEffect(() => {
    if (!enabled) {
      request.current?.abort();
      return;
    }
    const initial = window.setTimeout(() => void refresh(), 0);
    const interval = window.setInterval(() => void refresh(), refreshInterval);
    return () => {
      window.clearTimeout(initial);
      window.clearInterval(interval);
      request.current?.abort();
    };
  }, [enabled, refresh]);

  return { checkedAt, checking, error, refresh, status };
};

export type UpdateStatusState = ReturnType<typeof useUpdateStatus>;
