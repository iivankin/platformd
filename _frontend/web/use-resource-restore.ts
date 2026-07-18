import { useCallback, useEffect, useState } from "react";

import { fetchOperation, restoreBackupGeneration } from "@/api";
import type { Operation, RecoveryResourceKind } from "@/api";

const operationPollMilliseconds = 750;

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

interface ResourceRestoreInput {
  onSucceeded: () => Promise<void>;
  resourceID: string;
  resourceKind: RecoveryResourceKind;
  targetID: string;
}

export const useResourceRestore = ({
  onSucceeded,
  resourceID,
  resourceKind,
  targetID,
}: ResourceRestoreInput) => {
  const [error, setError] = useState<string>();
  const [operation, setOperation] = useState<Operation>();

  const accept = useCallback(
    async (current: Operation) => {
      setOperation(current);
      if (current.status === "succeeded") {
        setError(undefined);
        await onSucceeded();
      } else if (current.status !== "running") {
        setError(current.errorMessage || `Restore ended as ${current.status}`);
      }
    },
    [onSucceeded]
  );

  const start = useCallback(
    async (generationID: string) => {
      if (operation?.status === "running") {
        return;
      }
      setError(undefined);
      try {
        await accept(
          await restoreBackupGeneration(
            resourceKind,
            resourceID,
            targetID,
            generationID
          )
        );
      } catch (restoreError) {
        setError(errorText(restoreError, "Unable to restore generation"));
      }
    },
    [accept, operation?.status, resourceID, resourceKind, targetID]
  );

  useEffect(() => {
    if (!operation || operation.status !== "running") {
      return;
    }
    const controller = new AbortController();
    let inFlight = false;
    const poll = async () => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      try {
        await accept(await fetchOperation(operation.id, controller.signal));
      } catch (pollError) {
        if (
          !(
            pollError instanceof DOMException && pollError.name === "AbortError"
          )
        ) {
          setError(errorText(pollError, "Unable to read restore progress"));
        }
      } finally {
        inFlight = false;
      }
    };
    const interval = window.setInterval(
      () => void poll(),
      operationPollMilliseconds
    );
    return () => {
      controller.abort();
      window.clearInterval(interval);
    };
  }, [accept, operation]);

  return {
    error,
    operation,
    restoring: operation?.status === "running",
    start,
  };
};
