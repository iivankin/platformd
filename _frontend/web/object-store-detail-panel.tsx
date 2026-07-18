import { RefreshCw, Search } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  deleteObject,
  fetchObjects,
  fetchObjectStore,
  previewObject,
  uploadObject,
} from "@/api";
import type {
  ObjectMetadata,
  ObjectPage,
  ObjectPreview,
  ObjectStore,
} from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageStack } from "@/components/ui/page-stack";
import { ConnectionDetails } from "@/connection-details";
import {
  objectStoreConfiguration,
  objectStoreEndpoint,
} from "@/connection-values";
import {
  ObjectStorePreviewPane,
  ObjectStoreTable,
  ObjectStoreUploadBar,
} from "@/object-store-browser";
import type { ResourceNodeData } from "@/project-flow";
import { ResourceBackupPanel } from "@/resource-backup-panel";
import { ResourceLogs } from "@/resource-logs";
import { ResourceVariables } from "@/resource-variables";

export type ObjectStoreWorkspaceView =
  | "backups"
  | "logs"
  | "objects"
  | "settings"
  | "variables";

interface ObjectStoreDetailPanelProperties {
  data: ResourceNodeData;
  projectID: string;
  storeID: string;
  view: ObjectStoreWorkspaceView;
}

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export const ObjectStoreDetailPanel = ({
  data,
  projectID,
  storeID,
  view,
}: ObjectStoreDetailPanelProperties) => {
  const [resource, setResource] = useState<ObjectStore | null>(null);
  const [page, setPage] = useState<ObjectPage | null>(null);
  const [prefixInput, setPrefixInput] = useState("");
  const [prefix, setPrefix] = useState("");
  const [continuationToken, setContinuationToken] = useState("");
  const [tokenHistory, setTokenHistory] = useState<string[]>([]);
  const [selected, setSelected] = useState<ObjectMetadata | null>(null);
  const [preview, setPreview] = useState<ObjectPreview | null>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [loadedResource, loadedPage] = await Promise.all([
          fetchObjectStore(projectID, storeID, controller.signal),
          fetchObjects(
            projectID,
            storeID,
            { continuationToken, prefix },
            controller.signal
          ),
        ]);
        setResource(loadedResource);
        setPage(loadedPage);
        setError(null);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(errorText(loadError, "Unable to load object storage"));
      }
    };
    void load();
    return () => controller.abort();
  }, [continuationToken, prefix, projectID, refreshVersion, storeID]);

  useEffect(() => {
    if (!selected) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        setPreview(
          await previewObject(
            projectID,
            storeID,
            selected.objectKey,
            controller.signal
          )
        );
      } catch (previewError) {
        if (
          previewError instanceof DOMException &&
          previewError.name === "AbortError"
        ) {
          return;
        }
        setError(errorText(previewError, "Unable to preview object"));
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, selected, storeID]);

  const clearSelection = () => {
    setSelected(null);
    setPreview(null);
  };

  const applyPrefix = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPrefix(prefixInput);
    setContinuationToken("");
    setTokenHistory([]);
    clearSelection();
  };

  const submitUpload = async (key: string, file: File) => {
    if (busy) {
      return false;
    }
    setBusy(true);
    setError(null);
    try {
      await uploadObject(projectID, storeID, key, file);
      setContinuationToken("");
      setTokenHistory([]);
      setRefreshVersion((value) => value + 1);
      return true;
    } catch (uploadError) {
      setError(errorText(uploadError, "Upload failed"));
      return false;
    } finally {
      setBusy(false);
    }
  };

  const removeSelected = async () => {
    if (!selected || busy) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await deleteObject(projectID, storeID, selected.objectKey);
      clearSelection();
      setRefreshVersion((value) => value + 1);
    } catch (deleteError) {
      setError(errorText(deleteError, "Object deletion failed"));
    } finally {
      setBusy(false);
    }
  };

  const selectObject = (object: ObjectMetadata) => {
    setPreview(null);
    setSelected(object);
  };

  const previousPage = () => {
    const previous = tokenHistory.at(-1) ?? "";
    setTokenHistory((current) => current.slice(0, -1));
    setContinuationToken(previous);
    clearSelection();
  };

  const nextPage = () => {
    setTokenHistory((current) => [...current, continuationToken]);
    setContinuationToken(page?.nextContinuationToken ?? "");
    clearSelection();
  };

  const endpoint = resource
    ? objectStoreEndpoint(resource)
    : `http://${data.internalHostname}:9000`;

  return (
    <PageStack>
      {view === "settings" ? (
        <>
          <SectionCard className="grid shrink-0 grid-cols-3 text-[10px]">
            <div className="border-r border-border px-4 py-3">
              <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Endpoint
              </p>
              <p className="mt-1 truncate" title={endpoint}>
                {endpoint}
              </p>
            </div>
            <div className="border-r border-border px-4 py-3">
              <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Region
              </p>
              <p className="mt-1">{resource?.region ?? "us-east-1"}</p>
            </div>
            <div className="px-4 py-3">
              <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Access
              </p>
              <p className="mt-1">
                {resource?.publicHostname
                  ? "Public + internal"
                  : "Internal only"}
              </p>
            </div>
          </SectionCard>
          {resource ? (
            <ConnectionDetails
              description="S3 configuration remains available in this storage workspace."
              rows={[
                { label: "Endpoint", value: endpoint },
                { label: "Bucket", value: resource.bucketName },
                { label: "Access key", value: resource.accessKey },
                { label: "Secret key", value: resource.secret },
                {
                  label: "Configuration",
                  value: objectStoreConfiguration(resource),
                },
              ]}
            />
          ) : null}
        </>
      ) : null}

      {view === "variables" && resource ? (
        <ResourceVariables
          description="Reference these outputs from a service Variables tab. Values remain available here."
          variables={[
            { name: "S3_ENDPOINT", value: endpoint },
            { name: "S3_REGION", value: resource.region },
            { name: "S3_BUCKET", value: resource.bucketName },
            { name: "S3_ACCESS_KEY_ID", value: resource.accessKey },
            { name: "S3_SECRET_ACCESS_KEY", value: resource.secret },
          ]}
        />
      ) : null}

      {view === "backups" ? (
        <ResourceBackupPanel resourceID={storeID} resourceKind="object_store" />
      ) : null}

      {view === "objects" ? (
        <SectionCard>
          <header className="flex min-h-14 items-center justify-between border-b border-border px-4 py-3">
            <div>
              <h3 className="text-[10px] font-medium">Object browser</h3>
              <p className="mt-1 text-[9px] text-muted-foreground">
                Upload, inspect, and delete objects in this bucket.
              </p>
            </div>
            <Button
              aria-label="Refresh object list"
              onClick={() => setRefreshVersion((value) => value + 1)}
              size="icon"
              variant="ghost"
            >
              <RefreshCw />
            </Button>
          </header>
          <ObjectStoreUploadBar
            busy={busy}
            onUpload={submitUpload}
            prefix={prefix}
          />

          <form
            className="flex shrink-0 items-center gap-2 border-b border-border px-4 py-2"
            onSubmit={applyPrefix}
          >
            <Search className="size-3.5 text-muted-foreground" />
            <Input
              aria-label="Object key prefix"
              className="h-7 border-0 px-1 focus-visible:ring-0"
              onChange={(event) => setPrefixInput(event.target.value)}
              placeholder="Filter by exact key prefix"
              value={prefixInput}
            />
            <Button size="sm" type="submit" variant="ghost">
              Apply
            </Button>
          </form>

          <div className="grid min-h-0 flex-1 lg:grid-cols-[minmax(0,3fr)_minmax(20rem,2fr)]">
            <ObjectStoreTable
              canGoBack={tokenHistory.length > 0}
              onNext={nextPage}
              onPrevious={previousPage}
              onSelect={selectObject}
              page={page}
              selectedKey={selected?.objectKey}
            />
            <div className="min-h-0 overflow-auto">
              <ObjectStorePreviewPane
                busy={busy}
                key={selected?.objectKey ?? "empty"}
                onDelete={removeSelected}
                preview={preview}
                projectID={projectID}
                selected={selected}
                storeID={storeID}
              />
            </div>
          </div>
        </SectionCard>
      ) : null}

      {view === "logs" ? (
        <ResourceLogs
          description="Audited storage activity, refreshed every two seconds."
          kind="object_store"
          projectID={projectID}
          resourceID={storeID}
          title="Storage activity logs"
        />
      ) : null}

      {error ? (
        <p
          aria-live="polite"
          className="shrink-0 border-t border-border px-4 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </PageStack>
  );
};
