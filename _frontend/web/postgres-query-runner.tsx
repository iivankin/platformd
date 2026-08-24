import { Play } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { queryManagedPostgres } from "@/api";
import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { PostgresQueryEditor } from "@/postgres-query-editor";
import { PostgresResultTable } from "@/postgres-query-result";
import {
  postgresQueryCatalogFromResult,
  postgresQueryCatalogSQL,
} from "@/postgres-query-suggestions";
import type { PostgresQueryCatalogColumn } from "@/postgres-query-suggestions";

const catalogStatus = (
  loading: boolean,
  failed: boolean,
  columnCount: number
) => {
  if (loading) {
    return "Loading autocomplete…";
  }
  if (failed) {
    return "Autocomplete unavailable";
  }
  return `${columnCount} columns indexed`;
};

const queryCatalog = async (
  projectID: string,
  postgresID: string,
  signal?: AbortSignal
) =>
  postgresQueryCatalogFromResult(
    await queryManagedPostgres(
      projectID,
      postgresID,
      postgresQueryCatalogSQL,
      signal
    )
  );

export const PostgresQueryRunner = ({
  active,
  onSQLChange,
  postgresID,
  projectID,
  sql,
}: {
  active: boolean;
  onSQLChange: (sql: string) => void;
  postgresID: string;
  projectID: string;
  sql: string;
}) => {
  const [result, setResult] = useState<PostgresQueryResult | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string>();
  const [catalog, setCatalog] = useState<PostgresQueryCatalogColumn[]>([]);
  const [catalogLoading, setCatalogLoading] = useState(false);
  const [catalogError, setCatalogError] = useState(false);
  const [catalogInitialized, setCatalogInitialized] = useState(false);
  const catalogRetryOnActivation = useRef(false);
  const wasActive = useRef(false);

  useEffect(() => {
    const reactivated = active && !wasActive.current;
    wasActive.current = active;
    if (
      !active ||
      (catalogInitialized && !(catalogRetryOnActivation.current && reactivated))
    ) {
      return;
    }
    const controller = new AbortController();
    const loadInitialCatalog = async () => {
      try {
        setCatalog(
          await queryCatalog(projectID, postgresID, controller.signal)
        );
        catalogRetryOnActivation.current = false;
        setCatalogError(false);
      } catch (loadError) {
        if (loadError instanceof Error && loadError.name === "AbortError") {
          return;
        }
        catalogRetryOnActivation.current = true;
        setCatalogError(true);
      } finally {
        if (!controller.signal.aborted) {
          setCatalogLoading(false);
          setCatalogInitialized(true);
        }
      }
    };
    void loadInitialCatalog();
    return () => controller.abort();
  }, [active, catalogInitialized, postgresID, projectID]);

  const refreshCatalog = async () => {
    setCatalogLoading(true);
    try {
      setCatalog(await queryCatalog(projectID, postgresID));
      catalogRetryOnActivation.current = false;
      setCatalogError(false);
    } catch {
      catalogRetryOnActivation.current = true;
      setCatalogError(true);
    } finally {
      setCatalogLoading(false);
    }
  };

  const run = async () => {
    if (running || !sql.trim()) {
      return;
    }
    setRunning(true);
    try {
      const nextResult = await queryManagedPostgres(projectID, postgresID, sql);
      setResult(nextResult);
      setError(undefined);
      if (
        nextResult.statements.some((statement) =>
          /^(?:ALTER|COMMENT|CREATE|DROP|RENAME)\b/iu.test(statement.commandTag)
        )
      ) {
        void refreshCatalog();
      }
    } catch (queryError) {
      setError(
        queryError instanceof Error ? queryError.message : "SQL query failed"
      );
    } finally {
      setRunning(false);
    }
  };

  return (
    <div className="flex h-full min-h-0 flex-col overflow-hidden border border-border">
      <header className="flex h-10 shrink-0 items-center justify-between border-b border-border px-3">
        <div>
          <h3 className="text-[10px] font-medium">SQL editor</h3>
          <p className="text-[8px] text-muted-foreground">
            Owner session · changes are audited
          </p>
        </div>
        <div className="flex items-center gap-3">
          <span className="hidden text-[8px] text-muted-foreground md:inline">
            {catalogStatus(
              catalogLoading || (active && !catalogInitialized),
              catalogError,
              catalog.length
            )}
          </span>
          <span className="hidden text-[8px] text-muted-foreground sm:inline">
            {navigator.platform.includes("Mac") ? "⌘" : "Ctrl"} + Enter
          </span>
          <Button
            disabled={running || !sql.trim()}
            onClick={() => void run()}
            size="sm"
          >
            <Play /> {running ? "Running…" : "Run query"}
          </Button>
        </div>
      </header>
      <PostgresQueryEditor
        catalog={catalog}
        onChange={onSQLChange}
        onRun={() => void run()}
        sql={sql}
      />
      <PostgresResultTable result={result} />
      {error ? (
        <p
          aria-live="polite"
          className="border-t border-border px-4 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </div>
  );
};
