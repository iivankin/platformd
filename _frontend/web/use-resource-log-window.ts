import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { fetchResourceLogs, fetchTelemetryLogs } from "@/api";
import type { LogWindow, MetricScope, ResourceLogKind } from "@/api";
import type { LogFieldFilter } from "@/log-field-filter";
import type { TelemetryTimeRange } from "@/telemetry-query-state";
import { telemetryTimeBounds } from "@/telemetry-time-range";

const logPageSize = 200;
const refreshIntervalMilliseconds = 2000;

export type TelemetryLogSource =
  | {
      kind: "resource";
      projectID: string;
      resourceID: string;
      resourceKind: ResourceLogKind;
    }
  | {
      kind: "scope";
      scope: Exclude<MetricScope, { kind: "service" }>;
    };

const sourceIdentity = (source: TelemetryLogSource) =>
  source.kind === "resource"
    ? [source.kind, source.projectID, source.resourceKind, source.resourceID]
    : [
        source.kind,
        source.scope.kind,
        source.scope.kind === "project" ? source.scope.projectID : "",
      ];

const fetchWindow = (
  source: TelemetryLogSource,
  options: Parameters<typeof fetchResourceLogs>[3],
  signal?: AbortSignal
) =>
  source.kind === "resource"
    ? fetchResourceLogs(
        source.projectID,
        source.resourceKind,
        source.resourceID,
        options,
        signal
      )
    : fetchTelemetryLogs(source.scope, options, signal);

export const useTelemetryLogWindow = ({
  contains,
  deploymentID,
  fieldFilters,
  source,
  spanID,
  timeFrom,
  timeRange,
  timeTo,
  traceID,
}: {
  contains: string;
  deploymentID?: string;
  fieldFilters: LogFieldFilter[];
  source: TelemetryLogSource;
  spanID?: string;
  timeFrom: number | null;
  timeRange: TelemetryTimeRange;
  timeTo: number | null;
  traceID?: string;
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
        sourceIdentity(source),
        spanID,
        timeFrom,
        timeRange,
        timeTo,
        traceID,
      ]),
    [
      contains,
      deploymentID,
      fieldFilters,
      source,
      spanID,
      timeFrom,
      timeRange,
      timeTo,
      traceID,
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
        const nextWindow = await fetchWindow(
          source,
          {
            contains: contains || undefined,
            deploymentId: deploymentID,
            fieldFilters,
            spanId: spanID,
            ...bounds,
            limit: logPageSize,
            traceId: traceID,
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
    refreshVersion,
    source,
    spanID,
    timeFrom,
    timeRange,
    timeTo,
    traceID,
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
      const page = await fetchWindow(source, {
        contains: contains || undefined,
        cursor,
        deploymentId: deploymentID,
        fieldFilters,
        spanId: spanID,
        ...bounds,
        limit: logPageSize,
        traceId: traceID,
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
    loadingMore,
    source,
    spanID,
    timeFrom,
    timeRange,
    timeTo,
    traceID,
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
