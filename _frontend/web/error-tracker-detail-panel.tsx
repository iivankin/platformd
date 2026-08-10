import { LoaderCircle, Network, Save } from "lucide-react";
import { useEffect, useState } from "react";

import {
  errorTrackerConsolePath,
  fetchErrorTracker,
  updateErrorTrackerPublicAccess,
} from "@/api";
import type { ErrorTracker } from "@/api";
import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
import { TrackerApp } from "@/error-tracker/tracker-app";
import { ResourceBackupPanel } from "@/resource-backup-panel";

export type ErrorTrackerWorkspaceView = "backups" | "console" | "settings";

const ErrorTrackerPublicAccess = ({
  onSave,
  resource,
}: {
  onSave: (resource: ErrorTracker) => void;
  resource: ErrorTracker;
}) => {
  const [publicHostname, setPublicHostname] = useState(
    resource.publicHostname ?? ""
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const dirty = publicHostname !== (resource.publicHostname ?? "");
  const save = async () => {
    setBusy(true);
    setError("");
    try {
      onSave(
        await updateErrorTrackerPublicAccess(resource.projectId, resource.id, {
          expectedUpdatedAt: resource.updatedAt,
          publicHostname: publicHostname || undefined,
        })
      );
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to update public access"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
      <div className="px-5 py-4">
        <div className="flex items-center gap-2">
          <Network className="size-3.5 text-muted-foreground" />
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Public access
          </h3>
        </div>
        <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
          Optional HTTPS endpoint for Sentry clients. Changing it regenerates
          the DSNs shown by every application.
        </p>
      </div>
      <div className="border-t border-border lg:border-t-0 lg:border-l">
        <div className="border-b border-border px-5 py-4">
          <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Public hostname
          </span>
          <div className="mt-2">
            <CertificateHostnameCombobox
              ariaLabel="Error tracker public hostname"
              id="error-tracker-public-hostname"
              onChange={setPublicHostname}
              placeholder="errors.example.com"
              value={publicHostname}
            />
          </div>
        </div>
        <div className="flex items-center gap-3 px-5 py-4">
          <Button
            disabled={!dirty || busy}
            onClick={() => void save()}
            size="sm"
          >
            {busy ? <LoaderCircle className="animate-spin" /> : <Save />}
            Save public access
          </Button>
          {error ? (
            <p aria-live="polite" className="text-[9px] text-destructive">
              {error}
            </p>
          ) : null}
        </div>
      </div>
    </SectionCard>
  );
};

export const ErrorTrackerDetailPanel = ({
  projectID,
  trackerID,
  view,
}: {
  projectID: string;
  trackerID: string;
  view: ErrorTrackerWorkspaceView;
}) => {
  const [resource, setResource] = useState<ErrorTracker>();
  const [error, setError] = useState("");

  useEffect(() => {
    if (view !== "settings") {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        setResource(
          await fetchErrorTracker(projectID, trackerID, controller.signal)
        );
        setError("");
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
            : "Unable to load error tracker"
        );
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, trackerID, view]);

  if (view === "console") {
    return (
      <div className="h-full min-h-[28rem] overflow-hidden">
        <TrackerApp
          adminAuthentication={false}
          apiBasePath={errorTrackerConsolePath(projectID, trackerID)}
          embedded
          key={`${projectID}:${trackerID}`}
        />
      </div>
    );
  }

  return (
    <PageStack>
      {view === "settings" ? (
        <>
          <SectionCard className="grid shrink-0 grid-cols-3 text-[10px] max-md:grid-cols-1">
            <div className="border-r border-border px-4 py-3 max-md:border-r-0 max-md:border-b">
              <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Internal endpoint
              </p>
              <p className="mt-1 truncate" title={resource?.internalUrl}>
                {resource?.internalUrl ?? "Loading…"}
              </p>
            </div>
            <div className="border-r border-border px-4 py-3 max-md:border-r-0 max-md:border-b">
              <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Storage
              </p>
              <p className="mt-1">Dedicated volume</p>
            </div>
            <div className="px-4 py-3">
              <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Process
              </p>
              <p className="mt-1 capitalize">{resource?.status ?? "Loading"}</p>
            </div>
          </SectionCard>
          {resource ? (
            <ErrorTrackerPublicAccess
              key={resource.updatedAt}
              onSave={setResource}
              resource={resource}
            />
          ) : null}
        </>
      ) : null}
      {view === "backups" ? (
        <ResourceBackupPanel
          resourceID={trackerID}
          resourceKind="error_tracker"
        />
      ) : null}
      {error ? (
        <p
          aria-live="polite"
          className="border border-destructive/40 px-4 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </PageStack>
  );
};
