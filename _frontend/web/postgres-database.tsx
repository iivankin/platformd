import { Play, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { queryManagedPostgres } from "@/api";
import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

type DatabaseView = "config" | "data" | "stats";

const starterSQL = `SELECT
  schemaname AS schema,
  relname AS table,
  n_live_tup AS approximate_rows
FROM pg_stat_user_tables
ORDER BY schemaname, relname
LIMIT 100;`;

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
LIMIT 100;
SELECT extname AS name, extversion AS version FROM pg_extension ORDER BY extname;
SELECT name, default_version, installed_version, comment
FROM pg_available_extensions
ORDER BY name;`;

interface Cell {
  base64?: string;
  null?: boolean;
  text?: string;
}

const cellText = (cell?: Cell) => {
  if (!cell || cell.null) {
    return "—";
  }
  return cell.base64 === undefined
    ? (cell.text ?? "")
    : `base64:${cell.base64}`;
};

const ResultTable = ({ result }: { result: PostgresQueryResult | null }) => (
  <div className="min-h-0 flex-1 overflow-auto">
    {result?.statements.map((statement, statementIndex) => (
      <section
        className="border-b border-border"
        key={`${statementIndex.toString()}:${statement.commandTag}`}
      >
        <div className="flex items-center gap-3 border-b border-border px-4 py-2 text-[9px] text-muted-foreground">
          <span>{statement.commandTag || "Result"}</span>
          <span>{statement.rows.length.toLocaleString()} rows</span>
          {statement.truncated ? <span>bounded</span> : null}
        </div>
        {statement.columns.length > 0 ? (
          <table className="w-full border-collapse text-left text-[10px]">
            <thead>
              <tr className="border-b border-border">
                {statement.columns.map((column, columnIndex) => (
                  <th
                    className="border-r border-border px-3 py-2 font-medium last:border-r-0"
                    key={`${columnIndex.toString()}:${column.name}`}
                  >
                    {column.name}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {statement.rows.map((row, rowIndex) => (
                <tr
                  className="border-b border-border last:border-b-0"
                  key={rowIndex.toString()}
                >
                  {row.map((cell, cellIndex) => (
                    <td
                      className={cn(
                        "max-w-80 border-r border-border px-3 py-2 align-top break-all last:border-r-0",
                        cell.null && "text-muted-foreground italic"
                      )}
                      key={cellIndex.toString()}
                    >
                      {cellText(cell)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        ) : null}
      </section>
    ))}
  </div>
);

const PostgresStats = ({
  postgresID,
  projectID,
  view,
}: {
  postgresID: string;
  projectID: string;
  view: "config" | "stats";
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
  const installed = result?.statements[2];
  const available = result?.statements[3];

  const changeExtension = async (name: string, install: boolean) => {
    if (!/^[a-z][a-z0-9_]*$/u.test(name)) {
      setError("PostgreSQL returned an invalid extension name.");
      return;
    }
    setLoading(true);
    try {
      await queryManagedPostgres(
        projectID,
        postgresID,
        `${install ? "CREATE EXTENSION IF NOT EXISTS" : "DROP EXTENSION IF EXISTS"} "${name}";`
      );
      await load();
    } catch (changeError) {
      setError(
        changeError instanceof Error
          ? changeError.message
          : "Unable to change extension"
      );
      setLoading(false);
    }
  };

  if (view === "config") {
    const installedNames = new Set(
      installed?.rows.map((row) => cellText(row[0]))
    );
    return (
      <section>
        <header className="flex items-center justify-between border-b border-border px-5 py-3">
          <div>
            <h3 className="text-[10px] font-medium">Extensions</h3>
            <p className="mt-1 text-[9px] text-muted-foreground">
              Install trusted extensions available in this PostgreSQL image.
            </p>
          </div>
          <Button
            disabled={loading}
            onClick={() => void load()}
            size="icon"
            variant="ghost"
          >
            <RefreshCw />
          </Button>
        </header>
        {available?.rows.map((row) => {
          const name = cellText(row[0]);
          const isInstalled = installedNames.has(name);
          return (
            <div
              className="grid min-h-14 grid-cols-[12rem_7rem_minmax(0,1fr)_6rem] items-center border-b border-border px-5 text-[10px]"
              key={name}
            >
              <code>{name}</code>
              <span className="text-muted-foreground">{cellText(row[1])}</span>
              <span className="truncate pr-4 text-muted-foreground">
                {cellText(row[3])}
              </span>
              <Button
                disabled={loading}
                onClick={() => void changeExtension(name, !isInstalled)}
                size="sm"
                variant="outline"
              >
                {isInstalled ? "Uninstall" : "Install"}
              </Button>
            </div>
          );
        })}
        {error ? (
          <p className="border-b border-border px-5 py-3 text-[10px] text-destructive">
            {error}
          </p>
        ) : null}
      </section>
    );
  }

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
              {cellText(summary[index])}
              {label === "Cache hit" && cellText(summary[index]) !== "—"
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
        <ResultTable
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
  const [sql, setSQL] = useState(starterSQL);
  const [result, setResult] = useState<PostgresQueryResult | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string>();

  const run = async () => {
    setRunning(true);
    try {
      setResult(await queryManagedPostgres(projectID, postgresID, sql));
      setError(undefined);
    } catch (queryError) {
      setError(
        queryError instanceof Error ? queryError.message : "SQL query failed"
      );
    } finally {
      setRunning(false);
    }
  };

  return (
    <div>
      <nav
        className="flex min-h-10 border-b border-border px-4"
        aria-label="PostgreSQL database pages"
      >
        {(["data", "stats", "config"] as const).map((item) => (
          <button
            className={cn(
              "border-b-2 border-transparent px-4 text-[10px] text-muted-foreground capitalize",
              view === item && "border-foreground text-foreground"
            )}
            key={item}
            onClick={() => setView(item)}
            type="button"
          >
            {item}
          </button>
        ))}
      </nav>
      {view === "data" ? (
        <div className="flex min-h-[34rem] flex-col">
          <div className="relative h-52 shrink-0 border-b border-border">
            <textarea
              aria-label="PostgreSQL SQL editor"
              className="h-full w-full resize-none bg-background p-4 pr-24 font-mono text-[11px] leading-5 outline-none focus:bg-muted/10"
              onChange={(event) => setSQL(event.target.value)}
              spellCheck={false}
              value={sql}
            />
            <Button
              className="absolute top-3 right-3"
              disabled={running || !sql.trim()}
              onClick={() => void run()}
              size="sm"
            >
              <Play /> {running ? "Running…" : "Run"}
            </Button>
          </div>
          <ResultTable result={result} />
          {error ? (
            <p className="border-b border-border px-4 py-3 text-[10px] text-destructive">
              {error}
            </p>
          ) : null}
        </div>
      ) : (
        <PostgresStats
          postgresID={postgresID}
          projectID={projectID}
          view={view}
        />
      )}
    </div>
  );
};
