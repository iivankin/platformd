import { useCallback, useEffect, useState } from "react";

import { fetchManagedPostgres } from "@/api";
import type { ManagedPostgres } from "@/api";
import { ConnectionDetails } from "@/connection-details";
import { postgresConnectionURL } from "@/connection-values";
import { DatabaseVersionChange } from "@/database-version-change";
import { ManagedDeploymentHistory } from "@/managed-deployment-history";
import { PostgresDatabase } from "@/postgres-database";
import type { ResourceNodeData } from "@/project-flow";
import { ResourceBackupPanel } from "@/resource-backup-panel";
import { ResourceConsole } from "@/resource-console";
import { ResourceUsage } from "@/resource-usage";
import { ResourceVariables } from "@/resource-variables";
import { WorkspaceView } from "@/workspace-view";

export type PostgresWorkspaceView =
  | "backups"
  | "console"
  | "database"
  | "deployments"
  | "metrics"
  | "settings"
  | "variables";

interface PostgresDetailPanelProperties {
  data: ResourceNodeData;
  onChanged: () => void;
  postgresID: string;
  projectID: string;
  view: PostgresWorkspaceView;
}

export const PostgresDetailPanel = ({
  data,
  onChanged,
  postgresID,
  projectID,
  view,
}: PostgresDetailPanelProperties) => {
  const [resource, setResource] = useState<ManagedPostgres | null>(null);
  const [error, setError] = useState<string | null>(null);

  const loadResource = useCallback(
    async (signal?: AbortSignal) => {
      setResource(await fetchManagedPostgres(projectID, postgresID, signal));
    },
    [postgresID, projectID]
  );

  useEffect(() => {
    const controller = new AbortController();
    const loadInitialResource = async () => {
      try {
        const loaded = await fetchManagedPostgres(
          projectID,
          postgresID,
          controller.signal
        );
        setResource(loaded);
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
    void loadInitialResource();
    return () => controller.abort();
  }, [postgresID, projectID]);

  const refreshAfterVersionChange = useCallback(async () => {
    await loadResource();
    onChanged();
  }, [loadResource, onChanged]);

  const hostname = resource?.hostname ?? data.internalHostname;
  const variables = resource
    ? [
        { name: "PGHOST", value: hostname },
        { name: "PGPORT", value: "5432" },
        { name: "PGDATABASE", value: resource.databaseName },
        { name: "PGUSER", value: resource.ownerUsername },
        { name: "PGPASSWORD", value: resource.ownerPassword },
        { name: "DATABASE_URL", value: postgresConnectionURL(resource) },
      ]
    : [];

  return (
    <div>
      <WorkspaceView
        active={view}
        views={{
          backups: (
            <ResourceBackupPanel
              resourceID={postgresID}
              resourceKind="postgres"
            />
          ),
          console: (
            <ResourceConsole
              projectID={projectID}
              resourceID={postgresID}
              resourceKind="postgres"
              resourceName={data.name}
            />
          ),
          database: (
            <PostgresDatabase postgresID={postgresID} projectID={projectID} />
          ),
          deployments: (
            <ManagedDeploymentHistory
              kind="postgres"
              projectID={projectID}
              resourceID={postgresID}
            />
          ),
          metrics: (
            <ResourceUsage
              cpuMillicores={resource?.cpuMillicores}
              kind="postgres"
              memoryBytes={resource?.memoryBytes}
              resourceID={postgresID}
            />
          ),
          settings: (
            <>
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
              {resource ? (
                <ConnectionDetails
                  description="Full owner credentials remain available in Variables."
                  rows={[
                    {
                      label: "Connection URL",
                      value: postgresConnectionURL(resource),
                    },
                  ]}
                />
              ) : null}
            </>
          ),
          variables: (
            <ResourceVariables
              description="Reference these outputs from a service Variables tab. Values remain available here."
              variables={variables}
            />
          ),
        }}
      />
      {error ? (
        <p
          aria-live="polite"
          className="border-b border-border px-4 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </div>
  );
};
