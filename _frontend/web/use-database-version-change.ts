import { useCallback, useEffect, useState } from "react";

import {
  fetchDatabaseVersionOperation,
  fetchManagedImageTags,
  previewDatabaseVersion,
  startDatabaseVersionChange,
} from "@/api";
import type {
  DatabaseVersionPreview,
  ManagedImageEngine,
  ManagedImagePage,
  Operation,
} from "@/api";

const operationPollMilliseconds = 750;

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

interface DatabaseVersionChangeInput {
  engine: ManagedImageEngine;
  onSucceeded: () => Promise<void>;
  projectID: string;
  resourceID: string;
}

export const useDatabaseVersionChange = ({
  engine,
  onSucceeded,
  projectID,
  resourceID,
}: DatabaseVersionChangeInput) => {
  const [acknowledged, setAcknowledged] = useState(false);
  const [error, setError] = useState<string>();
  const [operation, setOperation] = useState<Operation>();
  const [preview, setPreview] = useState<DatabaseVersionPreview>();
  const [previewing, setPreviewing] = useState(false);
  const [tagError, setTagError] = useState<string>();
  const [tagPage, setTagPage] = useState<ManagedImagePage>();
  const [tagSearch, setTagSearch] = useState("");
  const [tagsLoading, setTagsLoading] = useState(false);
  const [targetTag, setTargetTag] = useState("");

  const selectTargetTag = useCallback((value: string) => {
    setTargetTag(value);
    setPreview(undefined);
    setAcknowledged(false);
    setError(undefined);
  }, []);

  const loadTags = useCallback(
    async (page: number, search: string, signal?: AbortSignal) => {
      setTagsLoading(true);
      setTagError(undefined);
      try {
        setTagPage(
          await fetchManagedImageTags(
            engine,
            { page, pageSize: 25, search: search.trim() },
            signal
          )
        );
      } catch (tagLoadError) {
        if (
          !(
            tagLoadError instanceof DOMException &&
            tagLoadError.name === "AbortError"
          )
        ) {
          setTagError(
            errorText(
              tagLoadError,
              "Unable to list tags; manual official tags still work."
            )
          );
        }
      } finally {
        setTagsLoading(false);
      }
    },
    [engine]
  );

  const previewTarget = useCallback(async () => {
    const selected = targetTag.trim();
    if (!selected || previewing || operation?.status === "running") {
      return;
    }
    setPreviewing(true);
    setError(undefined);
    setAcknowledged(false);
    try {
      setPreview(
        await previewDatabaseVersion(engine, projectID, resourceID, selected)
      );
    } catch (previewError) {
      setPreview(undefined);
      setError(errorText(previewError, "Unable to preview image change"));
    } finally {
      setPreviewing(false);
    }
  }, [engine, operation?.status, previewing, projectID, resourceID, targetTag]);

  const acceptOperation = useCallback(
    async (current: Operation) => {
      setOperation(current);
      if (current.status === "succeeded") {
        setError(undefined);
        setPreview(undefined);
        setAcknowledged(false);
        try {
          await onSucceeded();
        } catch (refreshError) {
          setError(
            errorText(
              refreshError,
              "Image changed, but refreshed resource details could not be loaded"
            )
          );
        }
      } else if (current.status !== "running") {
        setError(
          current.errorMessage || `Image change ended as ${current.status}`
        );
      }
    },
    [onSucceeded]
  );

  const start = useCallback(async () => {
    if (
      !preview?.ready ||
      !acknowledged ||
      preview.targetTag !== targetTag.trim() ||
      operation?.status === "running"
    ) {
      return;
    }
    setError(undefined);
    try {
      const result = await startDatabaseVersionChange(
        engine,
        projectID,
        resourceID,
        preview.targetTag,
        preview.targetDigest
      );
      await acceptOperation(result.operation);
    } catch (startError) {
      setError(errorText(startError, "Unable to start image change"));
    }
  }, [
    acceptOperation,
    acknowledged,
    engine,
    operation?.status,
    preview,
    projectID,
    resourceID,
    targetTag,
  ]);

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
        await acceptOperation(
          await fetchDatabaseVersionOperation(
            engine,
            projectID,
            resourceID,
            operation.id,
            controller.signal
          )
        );
      } catch (pollError) {
        if (
          !(
            pollError instanceof DOMException && pollError.name === "AbortError"
          )
        ) {
          setError(
            errorText(pollError, "Unable to read image-change progress")
          );
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
  }, [acceptOperation, engine, operation, projectID, resourceID]);

  return {
    acknowledged,
    error,
    loadTags,
    operation,
    preview,
    previewTarget,
    previewing,
    selectTargetTag,
    setAcknowledged,
    setTagSearch,
    start,
    tagError,
    tagPage,
    tagSearch,
    tagsLoading,
    targetTag,
  };
};

export type DatabaseVersionChangeState = ReturnType<
  typeof useDatabaseVersionChange
>;
