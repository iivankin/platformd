import { Plus, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import {
  fetchManagedRedis,
  mutateManagedRedis,
  previewManagedRedisKey,
  scanManagedRedisKeys,
} from "@/api";
import type {
  ManagedRedis,
  RedisKey,
  RedisMutationInput,
  RedisPreview,
} from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ConnectionDetails } from "@/connection-details";
import { redisConnectionURL } from "@/connection-values";
import { DatabaseVersionChange } from "@/database-version-change";
import { cn } from "@/lib/utils";
import { ManagedDeploymentHistory } from "@/managed-deployment-history";
import type { ResourceNodeData } from "@/project-flow";
import { formatBytes, formatTTL } from "@/redis-data-utils";
import { RedisKeyEditor } from "@/redis-key-editor";
import { RedisNewKeyForm } from "@/redis-new-key-form";
import { RedisPersistenceStatus } from "@/redis-persistence-status";
import { RedisStats } from "@/redis-stats";
import { ResourceBackupPanel } from "@/resource-backup-panel";
import { ResourceConsole } from "@/resource-console";
import { ResourceUsage } from "@/resource-usage";
import { ResourceVariables } from "@/resource-variables";
import { WorkspaceView } from "@/workspace-view";

export type RedisWorkspaceView =
  | "backups"
  | "console"
  | "database"
  | "deployments"
  | "metrics"
  | "settings"
  | "variables";

interface RedisDetailPanelProperties {
  data: ResourceNodeData;
  onChanged: () => void;
  projectID: string;
  redisID: string;
  view: RedisWorkspaceView;
}

interface RedisVersionChangeProperties {
  onSucceeded: () => Promise<void>;
  projectID: string;
  redisID: string;
  resource: ManagedRedis | null;
}

const RedisVersionChange = ({
  onSucceeded,
  projectID,
  redisID,
  resource,
}: RedisVersionChangeProperties) => {
  if (!resource) {
    return null;
  }
  return (
    <DatabaseVersionChange
      activeDigest={resource.imageDigest}
      activeTag={resource.imageTag}
      engine="redis"
      onSucceeded={onSucceeded}
      projectID={projectID}
      resourceID={redisID}
    />
  );
};

const RedisResourceUsage = ({
  redisID,
  resource,
}: {
  redisID: string;
  resource: ManagedRedis | null;
}) => (
  <ResourceUsage
    cpuMillicores={resource?.cpuMillicores}
    kind="redis"
    memoryBytes={resource?.memoryBytes}
    resourceID={redisID}
  />
);

const RedisOverview = ({
  data,
  onVersionChanged,
  projectID,
  redisID,
  resource,
}: {
  data: ResourceNodeData;
  onVersionChanged: () => Promise<void>;
  projectID: string;
  redisID: string;
  resource: ManagedRedis | null;
}) => (
  <>
    <section className="grid grid-cols-2 border-b border-border text-[10px] sm:grid-cols-4">
      <div className="border-r border-border px-4 py-3">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Endpoint
        </p>
        <p className="mt-1 truncate">
          {resource?.hostname ?? data.internalHostname}:6379
        </p>
      </div>
      <div className="border-r border-border px-4 py-3">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Image
        </p>
        <p className="mt-1 truncate">
          redis:{resource?.imageTag ?? data.imageReference}
        </p>
      </div>
      <div className="border-r border-border px-4 py-3">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          CPU
        </p>
        <p className="mt-1">
          {resource?.cpuMillicores ? `${resource.cpuMillicores}m` : "Unlimited"}
        </p>
      </div>
      <div className="px-4 py-3">
        <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
          Memory
        </p>
        <p className="mt-1">
          {resource?.memoryBytes
            ? formatBytes(resource.memoryBytes)
            : "Unlimited"}
        </p>
      </div>
    </section>

    {resource ? (
      <ConnectionDetails
        description="Available whenever you return to this Redis resource."
        rows={[
          {
            label: "Connection URL",
            value: redisConnectionURL(resource),
          },
          { label: "Password", value: resource.password },
        ]}
      />
    ) : null}

    <RedisPersistenceStatus projectID={projectID} redisID={redisID} />
    <RedisVersionChange
      onSucceeded={onVersionChanged}
      projectID={projectID}
      redisID={redisID}
      resource={resource}
    />
  </>
);

export const RedisDetailPanel = ({
  data,
  onChanged,
  projectID,
  redisID,
  view,
}: RedisDetailPanelProperties) => {
  const [resource, setResource] = useState<ManagedRedis | null>(null);
  const [keys, setKeys] = useState<RedisKey[]>([]);
  const [cursor, setCursor] = useState("0");
  const [match, setMatch] = useState("");
  const [appliedMatch, setAppliedMatch] = useState("");
  const [selectedKey, setSelectedKey] = useState<RedisKey | null>(null);
  const [preview, setPreview] = useState<RedisPreview | null>(null);
  const [newKeyOpen, setNewKeyOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [databaseView, setDatabaseView] = useState<"config" | "data" | "stats">(
    "data"
  );
  const [error, setError] = useState<string | null>(null);
  const hasMoreKeys = cursor !== "0";

  const variables = resource
    ? [
        { name: "REDISHOST", value: resource.hostname },
        { name: "REDISPORT", value: "6379" },
        { name: "REDISPASSWORD", value: resource.password },
        { name: "REDIS_URL", value: redisConnectionURL(resource) },
      ]
    : [];

  const loadResource = useCallback(
    async (signal?: AbortSignal) => {
      setResource(await fetchManagedRedis(projectID, redisID, signal));
    },
    [projectID, redisID]
  );

  const loadKeys = useCallback(
    async (
      options: { append?: boolean; cursor?: string; signal?: AbortSignal } = {}
    ) => {
      const page = await scanManagedRedisKeys(
        projectID,
        redisID,
        { count: 50, cursor: options.cursor ?? "0", match: appliedMatch },
        options.signal
      );
      setKeys((current) =>
        options.append ? [...current, ...page.keys] : page.keys
      );
      setCursor(page.nextCursor);
    },
    [appliedMatch, projectID, redisID]
  );

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        await Promise.all([
          loadResource(controller.signal),
          loadKeys({ signal: controller.signal }),
        ]);
        setError(null);
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
            : "Unable to load managed Redis"
        );
      }
    };
    void load();
    return () => controller.abort();
  }, [loadKeys, loadResource]);

  const refreshAfterVersionChange = useCallback(async () => {
    await Promise.all([loadResource(), loadKeys()]);
    onChanged();
  }, [loadKeys, loadResource, onChanged]);

  const selectKey = async (key: RedisKey) => {
    setSelectedKey(key);
    setPreview(null);
    setError(null);
    try {
      setPreview(
        await previewManagedRedisKey(projectID, redisID, key.keyBase64)
      );
    } catch (previewError) {
      setError(
        previewError instanceof Error
          ? previewError.message
          : "Unable to preview Redis value"
      );
    }
  };

  const mutate = async (input: RedisMutationInput) => {
    if (busy) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const result = await mutateManagedRedis(projectID, redisID, input);
      if (!result.auditRecorded) {
        setError(
          "Mutation succeeded, but its audit event could not be recorded."
        );
      }
      await loadKeys();
      if (input.operation === "key_delete") {
        setSelectedKey(null);
        setPreview(null);
      } else if (selectedKey) {
        setPreview(
          await previewManagedRedisKey(
            projectID,
            redisID,
            selectedKey.keyBase64
          )
        );
      }
      setNewKeyOpen(false);
      onChanged();
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <WorkspaceView
        active={view}
        views={{
          backups: (
            <ResourceBackupPanel resourceID={redisID} resourceKind="redis" />
          ),
          console: (
            <ResourceConsole
              projectID={projectID}
              resourceID={redisID}
              resourceKind="redis"
              resourceName={data.name}
            />
          ),
          database: (
            <>
              <nav
                className="flex min-h-10 border-b border-border px-4"
                aria-label="Redis database pages"
              >
                {(["data", "stats", "config"] as const).map((item) => (
                  <button
                    className={cn(
                      "border-b-2 border-transparent px-4 text-[10px] text-muted-foreground capitalize",
                      databaseView === item &&
                        "border-foreground text-foreground"
                    )}
                    key={item}
                    onClick={() => setDatabaseView(item)}
                    type="button"
                  >
                    {item}
                  </button>
                ))}
              </nav>
              {databaseView === "data" ? (
                <>
                  <section className="border-b border-border px-4 py-3">
                    <form
                      className="flex gap-2"
                      onSubmit={(event) => {
                        event.preventDefault();
                        setSelectedKey(null);
                        setPreview(null);
                        if (match === appliedMatch) {
                          void loadKeys();
                        } else {
                          setAppliedMatch(match);
                        }
                      }}
                    >
                      <Input
                        aria-label="Redis SCAN match"
                        className="min-w-0 flex-1"
                        onChange={(event) => setMatch(event.target.value)}
                        placeholder="SCAN match, e.g. user:*"
                        value={match}
                      />
                      <Button size="sm" type="submit" variant="outline">
                        <RefreshCw />
                        Scan
                      </Button>
                      <Button
                        onClick={() => setNewKeyOpen((open) => !open)}
                        size="sm"
                        type="button"
                      >
                        <Plus />
                        Key
                      </Button>
                    </form>
                  </section>

                  {newKeyOpen ? (
                    <RedisNewKeyForm
                      busy={busy}
                      onCancel={() => setNewKeyOpen(false)}
                      onMutate={mutate}
                    />
                  ) : null}

                  <section className="grid min-h-52 grid-cols-[minmax(13rem,0.8fr)_minmax(18rem,1.2fr)] border-b border-border">
                    <div className="min-w-0 border-r border-border">
                      <div className="grid grid-cols-[1fr_auto_auto] border-b border-border px-3 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                        <span>Key</span>
                        <span>TTL</span>
                        <span className="ml-3">Size</span>
                      </div>
                      <div className="max-h-[30rem] overflow-y-auto">
                        {keys.map((key) => (
                          <button
                            className={`grid w-full grid-cols-[1fr_auto_auto] items-center border-b border-border px-3 py-2.5 text-left text-[10px] hover:bg-muted/40 ${
                              selectedKey?.keyBase64 === key.keyBase64
                                ? "bg-muted/60"
                                : ""
                            }`}
                            key={key.keyBase64}
                            onClick={() => void selectKey(key)}
                            type="button"
                          >
                            <span className="min-w-0 truncate font-medium">
                              {key.keyText ?? `base64:${key.keyBase64}`}
                              <span className="ml-2 text-[8px] font-normal text-muted-foreground">
                                {key.type}
                              </span>
                            </span>
                            <span className="text-[9px] text-muted-foreground">
                              {formatTTL(key.expiresInMillis)}
                            </span>
                            <span className="ml-3 text-[9px] text-muted-foreground">
                              {formatBytes(key.sizeBytes)}
                            </span>
                          </button>
                        ))}
                        {hasMoreKeys ? (
                          <Button
                            className="m-3"
                            onClick={() =>
                              void loadKeys({ append: true, cursor })
                            }
                            size="sm"
                            variant="outline"
                          >
                            Continue SCAN
                          </Button>
                        ) : null}
                        {keys.length === 0 ? (
                          <p className="px-3 py-6 text-center text-[10px] text-muted-foreground">
                            No keys in this scan page.
                          </p>
                        ) : null}
                      </div>
                    </div>
                    <div className="min-w-0">
                      {selectedKey && preview ? (
                        <RedisKeyEditor
                          busy={busy}
                          key={`${selectedKey.keyBase64}:${preview.type}:${preview.length}:${preview.items[0]?.values[0]?.base64 ?? ""}`}
                          keyBase64={selectedKey.keyBase64}
                          onMutate={mutate}
                          preview={preview}
                        />
                      ) : (
                        <div className="grid min-h-52 place-items-center px-6 text-center text-[10px] leading-5 text-muted-foreground">
                          Select a key to inspect and edit its value.
                        </div>
                      )}
                    </div>
                  </section>
                </>
              ) : null}
              {databaseView === "stats" ? (
                <RedisStats projectID={projectID} redisID={redisID} />
              ) : null}
              {databaseView === "config" ? (
                <RedisPersistenceStatus
                  projectID={projectID}
                  redisID={redisID}
                />
              ) : null}
            </>
          ),
          deployments: (
            <ManagedDeploymentHistory
              kind="redis"
              projectID={projectID}
              resourceID={redisID}
            />
          ),
          metrics: <RedisResourceUsage redisID={redisID} resource={resource} />,
          settings: (
            <RedisOverview
              data={data}
              onVersionChanged={refreshAfterVersionChange}
              projectID={projectID}
              redisID={redisID}
              resource={resource}
            />
          ),
          variables: (
            <ResourceVariables
              description="Reference these outputs from a service Variables tab. Values remain available here."
              variables={variables}
            />
          ),
        }}
      />

      {data.statusMessage || error ? (
        <section className="border-b border-border px-4 py-3 text-[10px] text-destructive">
          {error ?? data.statusMessage}
        </section>
      ) : null}
    </div>
  );
};
