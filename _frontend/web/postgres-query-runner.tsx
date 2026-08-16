import { Play } from "lucide-react";
import type { KeyboardEvent } from "react";
import { useState } from "react";

import { queryManagedPostgres } from "@/api";
import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { PostgresResultTable } from "@/postgres-query-result";

const starterSQL = `SELECT
  schemaname AS schema,
  relname AS table,
  n_live_tup AS approximate_rows
FROM pg_stat_user_tables
ORDER BY schemaname, relname
LIMIT 100;`;

export const PostgresQueryRunner = ({
  initialSQL,
  postgresID,
  projectID,
}: {
  initialSQL?: string;
  postgresID: string;
  projectID: string;
}) => {
  const [sql, setSQL] = useState(initialSQL ?? starterSQL);
  const [result, setResult] = useState<PostgresQueryResult | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string>();

  const run = async () => {
    if (running || !sql.trim()) {
      return;
    }
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

  const runWithShortcut = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      void run();
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
      <div className="relative h-56 shrink-0 border-b border-border">
        <textarea
          aria-label="PostgreSQL SQL editor"
          className="h-full w-full resize-none bg-background p-4 font-mono text-[11px] leading-5 outline-none focus:bg-muted/10"
          onChange={(event) => setSQL(event.target.value)}
          onKeyDown={runWithShortcut}
          spellCheck={false}
          value={sql}
        />
      </div>
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
