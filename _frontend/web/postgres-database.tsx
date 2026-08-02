import { RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { queryManagedPostgres } from "@/api";
import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { PostgresDataBrowser } from "@/postgres-data-browser";
import { PostgresExtensions } from "@/postgres-extensions";
import { PostgresResultTable, postgresCellText } from "@/postgres-query-result";
import { PostgresQueryRunner } from "@/postgres-query-runner";

type DatabaseView = "data" | "extensions" | "query" | "stats";

const inspectionSQL = `SELECT
  current_setting('server_version') AS version,
  (SELECT count(*)::text FROM pg_stat_activity) AS connections,
  (SELECT count(*)::text FROM pg_stat_activity WHERE state = 'active') AS active,
  (SELECT count(*)::text FROM pg_stat_activity WHERE state = 'idle') AS idle,
  (SELECT count(*)::text FROM pg_stat_activity WHERE state = 'idle in transaction') AS idle_in_transaction,
  (SELECT round(100 * sum(blks_hit)::numeric / nullif(sum(blks_hit + blks_read), 0), 1)::text FROM pg_stat_database) AS cache_hit_percent;
SELECT
  relname AS table,
  n_live_tup::text AS rows,
  pg_size_pretty(pg_total_relation_size(relid)) AS total,
  pg_size_pretty(pg_relation_size(relid)) AS data,
  pg_size_pretty(pg_indexes_size(relid)) AS indexes,
  n_dead_tup::text AS dead_rows,
  COALESCE(last_autovacuum::text, last_vacuum::text, 'never') AS last_vacuum
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(relid) DESC
LIMIT 100;`;

const PostgresStats = ({
  postgresID,
  projectID,
}: {
  postgresID: string;
  projectID: string;
}) => {
  const [result, setResult] = useState<PostgresQueryResult | null>(null);
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);

  const load = useCallback(async () => {
    setLoading(true);
    try {
      setResult(
        await queryManagedPostgres(projectID, postgresID, inspectionSQL)
      );
      setError(undefined);
    } catch (loadError) {
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to inspect PostgreSQL"
      );
    } finally {
      setLoading(false);
    }
  }, [postgresID, projectID]);

  useEffect(() => {
    const loadInitialInspection = async () => {
      try {
        const inspected = await queryManagedPostgres(
          projectID,
          postgresID,
          inspectionSQL
        );
        setResult(inspected);
        setError(undefined);
      } catch (loadError) {
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to inspect PostgreSQL"
        );
      } finally {
        setLoading(false);
      }
    };
    void loadInitialInspection();
  }, [postgresID, projectID]);

  const summary = result?.statements[0]?.rows[0] ?? [];
  const tables = result?.statements[1];

  const labels = [
    "Version",
    "Connections",
    "Active",
    "Idle",
    "Idle in tx",
    "Cache hit",
  ];
  return (
    <section>
      <div className="grid grid-cols-3 border-b border-border lg:grid-cols-6">
        {labels.map((label, index) => (
          <div
            className="border-r border-border px-4 py-3 last:border-r-0"
            key={label}
          >
            <p className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
              {label}
            </p>
            <p className="mt-1 text-xs">
              {postgresCellText(summary[index])}
              {label === "Cache hit" && postgresCellText(summary[index]) !== "—"
                ? "%"
                : ""}
            </p>
          </div>
        ))}
      </div>
      <header className="flex items-center justify-between border-b border-border px-5 py-3">
        <h3 className="text-[10px] font-medium">Tables and vacuum health</h3>
        <Button
          disabled={loading}
          onClick={() => void load()}
          size="icon"
          variant="ghost"
        >
          <RefreshCw />
        </Button>
      </header>
      {tables ? (
        <PostgresResultTable
          result={{
            auditRecorded: true,
            statements: [tables],
            truncated: false,
          }}
        />
      ) : null}
      {error ? (
        <p className="border-b border-border px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </section>
  );
};

export const PostgresDatabase = ({
  postgresID,
  projectID,
}: {
  postgresID: string;
  projectID: string;
}) => {
  const [view, setView] = useState<DatabaseView>("data");
  const [queryDraft, setQueryDraft] = useState<string>();

  const renderView = () => {
    if (view === "extensions") {
      return (
        <PostgresExtensions postgresID={postgresID} projectID={projectID} />
      );
    }
    if (view === "stats") {
      return <PostgresStats postgresID={postgresID} projectID={projectID} />;
    }
    if (view === "query") {
      return (
        <PostgresQueryRunner
          initialSQL={queryDraft}
          postgresID={postgresID}
          projectID={projectID}
        />
      );
    }
    return null;
  };

  const views: { label: string; value: DatabaseView }[] = [
    { label: "Data", value: "data" },
    { label: "Query", value: "query" },
    { label: "Stats", value: "stats" },
    { label: "Extensions", value: "extensions" },
  ];

  return (
    <div>
      <nav
        className="flex min-h-10 border-b border-border px-4"
        aria-label="PostgreSQL database pages"
      >
        {views.map((item) => (
          <button
            className={cn(
              "border-b-2 border-transparent px-4 text-[10px] text-muted-foreground",
              view === item.value && "border-foreground text-foreground"
            )}
            key={item.value}
            onClick={() => setView(item.value)}
            type="button"
          >
            {item.label}
          </button>
        ))}
      </nav>
      <div hidden={view !== "data"}>
        <PostgresDataBrowser
          onOpenInQuery={(sql) => {
            setQueryDraft(sql);
            setView("query");
          }}
          postgresID={postgresID}
          projectID={projectID}
        />
      </div>
      {renderView()}
    </div>
  );
};
