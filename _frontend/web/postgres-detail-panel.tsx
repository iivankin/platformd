import { Database, Play, X } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { fetchManagedPostgres, queryManagedPostgres } from "@/api";
import type { ManagedPostgres, PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { DatabaseVersionChange } from "@/database-version-change";
import type { ResourceNodeData } from "@/project-flow";
import { ResourceBackupPanel } from "@/resource-backup-panel";
import { ResourceUsage } from "@/resource-usage";

interface PostgresDetailPanelProperties {
  data: ResourceNodeData;
  onChanged: () => void;
  onClose: () => void;
  postgresID: string;
  projectID: string;
}

const starterSQL = `SELECT
  schemaname AS schema,
  relname AS table,
  n_live_tup AS approximate_rows
FROM pg_stat_user_tables
ORDER BY schemaname, relname
LIMIT 100;`;

const cellText = (cell: { base64?: string; null?: boolean; text?: string }) => {
  if (cell.null) {
    return "NULL";
  }
  if (cell.base64 !== undefined) {
    return `base64:${cell.base64}`;
  }
  return cell.text ?? "";
};

export const PostgresDetailPanel = ({
  data,
  onChanged,
  onClose,
  postgresID,
  projectID,
}: PostgresDetailPanelProperties) => {
  const [resource, setResource] = useState<ManagedPostgres | null>(null);
  const [sql, setSQL] = useState(starterSQL);
  const [result, setResult] = useState<PostgresQueryResult | null>(null);
  const [running, setRunning] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadResource = useCallback(
    async (signal?: AbortSignal) => {
      setResource(await fetchManagedPostgres(projectID, postgresID, signal));
    },
    [postgresID, projectID]
  );

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        await loadResource(controller.signal);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load PostgreSQL"
        );
      }
    };
    void load();
    return () => controller.abort();
  }, [loadResource]);

  const refreshAfterVersionChange = useCallback(async () => {
    await loadResource();
    onChanged();
  }, [loadResource, onChanged]);

  const run = async () => {
    if (running) {
      return;
    }
    setRunning(true);
    setError(null);
    try {
      const output = await queryManagedPostgres(projectID, postgresID, sql);
      setResult(output);
      if (!output.auditRecorded) {
        setError("Query completed, but its audit event could not be recorded.");
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
    <aside className="absolute inset-y-0 right-0 z-20 flex w-full max-w-4xl flex-col border-l border-border bg-background shadow-[-8px_0_24px_oklch(0_0_0/5%)]">
      <div className="flex h-12 shrink-0 items-center border-b border-border px-4">
        <Database className="size-4 text-muted-foreground" />
        <div className="ml-2 min-w-0">
          <h2 className="truncate text-xs font-medium">{data.name}</h2>
          <p className="text-[9px] text-muted-foreground">
            {resource?.databaseName ?? "Managed PostgreSQL"}
          </p>
        </div>
        <Button
          aria-label="Close PostgreSQL workspace"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>

      <div className="grid shrink-0 grid-cols-3 border-b border-border text-[10px]">
        <div className="border-r border-border px-4 py-3">
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Endpoint
          </p>
          <p className="mt-1 truncate">
            {resource?.hostname ?? data.internalHostname}:5432
          </p>
        </div>
        <div className="border-r border-border px-4 py-3">
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Owner
          </p>
          <p className="mt-1 truncate">{resource?.ownerUsername ?? "—"}</p>
        </div>
        <div className="px-4 py-3">
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Image
          </p>
          <p className="mt-1 truncate">postgres:{resource?.imageTag ?? "—"}</p>
        </div>
      </div>

      <ResourceUsage
        cpuMillicores={resource?.cpuMillicores}
        kind="postgres"
        memoryBytes={resource?.memoryBytes}
        resourceID={postgresID}
      />

      <div className="max-h-[45vh] shrink-0 overflow-y-auto">
        <ResourceBackupPanel resourceID={postgresID} resourceKind="postgres" />
      </div>

      {resource ? (
        <DatabaseVersionChange
          activeDigest={resource.imageDigest}
          activeTag={resource.imageTag}
          engine="postgres"
          onSucceeded={refreshAfterVersionChange}
          projectID={projectID}
          resourceID={postgresID}
        />
      ) : null}

      <div className="flex min-h-0 flex-1 flex-col">
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
            disabled={running || sql.trim() === ""}
            onClick={() => void run()}
            size="sm"
          >
            <Play />
            {running ? "Running…" : "Run"}
          </Button>
        </div>

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
                            className={`max-w-80 border-r border-border px-3 py-2 align-top break-all last:border-r-0 ${cell.null ? "text-muted-foreground italic" : ""}`}
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
          {result && result.statements.length === 0 ? (
            <p className="px-4 py-6 text-[10px] text-muted-foreground">
              Query completed without a result set.
            </p>
          ) : null}
          {result ? null : (
            <p className="px-4 py-6 text-[10px] leading-5 text-muted-foreground">
              Run arbitrary owner-level SQL here. SELECT, INSERT, UPDATE,
              DELETE, DDL, transactions, and multi-statement scripts are
              supported.
            </p>
          )}
        </div>
      </div>
      {error ? (
        <p
          aria-live="polite"
          className="shrink-0 border-t border-border px-4 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </aside>
  );
};
