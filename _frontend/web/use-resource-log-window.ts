import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { fetchResourceLogs } from "@/api";
import type { LogWindow, ResourceLogKind } from "@/api";
import type { LogFieldFilter } from "@/log-field-filter";
import type { TelemetryTimeRange } from "@/telemetry-query-state";
import { telemetryTimeBounds } from "@/telemetry-time-range";

const logPageSize = 200;
const refreshIntervalMilliseconds = 2000;

export const useResourceLogWindow = ({
  contains,
  deploymentID,
  fieldFilters,
  kind,
  projectID,
  resourceID,
  timeFrom,
  timeRange,
  timeTo,
}: {
  contains: string;
  deploymentID?: string;
  fieldFilters: LogFieldFilter[];
  kind: ResourceLogKind;
  projectID: string;
  resourceID: string;
  timeFrom: number | null;
  timeRange: TelemetryTimeRange;
  timeTo: number | null;
}) => {
  const [window, setWindow] = useState<LogWindow>();
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [live, setLive] = useState(true);
  const [error, setError] = useState<string>();
  const loadedQueryRef = useRef("");
  const paginationActiveRef = useRef(false);
  const activeQueryKey = useMemo(
    () =>
      JSON.stringify([
        contains,
        deploymentID,
        fieldFilters,
        kind,
        projectID,
        resourceID,
        timeFrom,
        timeRange,
        timeTo,
      ]),
    [
      contains,
      deploymentID,
      fieldFilters,
      kind,
      projectID,
      resourceID,
      timeFrom,
      timeRange,
      timeTo,
    ]
  );
  const activeQueryRef = useRef(activeQueryKey);

  useEffect(() => {
    activeQueryRef.current = activeQueryKey;
    paginationActiveRef.current = false;
  }, [activeQueryKey]);

  const refresh = useCallback(() => {
    paginationActiveRef.current = false;
    setRefreshVersion((value) => value + 1);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const requestKey = activeQueryKey;
    const load = async () => {
      try {
        setLoading(true);
        if (loadedQueryRef.current !== requestKey) {
          loadedQueryRef.current = requestKey;
          setLoadingMore(false);
          setWindow(undefined);
        }
        const bounds = telemetryTimeBounds({
          from: timeFrom,
          range: timeRange,
          to: timeTo,
        });
        const nextWindow = await fetchResourceLogs(
          projectID,
          kind,
          resourceID,
          {
            contains: contains || undefined,
            deploymentId: deploymentID,
            fieldFilters,
            ...bounds,
            limit: logPageSize,
          },
          controller.signal
        );
        if (
          activeQueryRef.current === requestKey &&
          !paginationActiveRef.current
        ) {
          setWindow(nextWindow);
        }
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load resource logs"
          );
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [
    activeQueryKey,
    contains,
    deploymentID,
    fieldFilters,
    kind,
    projectID,
    refreshVersion,
    resourceID,
    timeFrom,
    timeRange,
    timeTo,
  ]);

  useEffect(() => {
    if (!live || loading) {
      return;
    }
    const timer = setTimeout(refresh, refreshIntervalMilliseconds);
    return () => clearTimeout(timer);
  }, [live, loading, refresh]);

  const loadMore = useCallback(async () => {
    const cursor = window?.nextCursor;
    if (!cursor || loadingMore) {
      return;
    }
    paginationActiveRef.current = true;
    setLive(false);
    setLoadingMore(true);
    const requestKey = activeQueryKey;
    try {
      const bounds = telemetryTimeBounds({
        from: timeFrom,
        range: timeRange,
        to: timeTo,
      });
      const page = await fetchResourceLogs(projectID, kind, resourceID, {
        contains: contains || undefined,
        cursor,
        deploymentId: deploymentID,
        fieldFilters,
        ...bounds,
        limit: logPageSize,
      });
      if (activeQueryRef.current !== requestKey) {
        return;
      }
      setWindow((current) => {
        if (!current || current.nextCursor !== cursor) {
          return current;
        }
        return { ...page, records: [...current.records, ...page.records] };
      });
      setError(undefined);
    } catch (loadError) {
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to load more logs"
      );
    } finally {
      if (activeQueryRef.current === requestKey) {
        setLoadingMore(false);
      }
    }
  }, [
    activeQueryKey,
    contains,
    deploymentID,
    fieldFilters,
    kind,
    loadingMore,
    projectID,
    resourceID,
    timeFrom,
    timeRange,
    timeTo,
    window?.nextCursor,
  ]);

  return {
    error,
    live,
    loadMore,
    loading,
    loadingMore,
    refresh,
    setLive,
    window,
  };
};
