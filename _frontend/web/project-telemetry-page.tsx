import { LoaderCircle } from "lucide-react";
import { useQueryState, useQueryStates } from "nuqs";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Navigate, useLocation, useNavigate, useParams } from "react-router";

import { AiOverviewView } from "@/ai-overview";
import { fetchProjectCanvas } from "@/api";
import type { MetricScope, ProjectCanvas, ScopedIssue } from "@/api";
import { ScopedTelemetryLogs } from "@/resource-logs";
import { ProjectUsage } from "@/resource-usage";
import { ScopedErrors } from "@/scoped-errors";
import { TelemetryTraces } from "@/service-traces";
import { logQueryParsers, telemetryViewParser } from "@/telemetry-query-state";
import { TelemetryWorkspace } from "@/telemetry-workspace";

export const ProjectTelemetryPage = () => {
  const { projectID = "" } = useParams();
  const location = useLocation();
  const navigate = useNavigate();
  const [canvas, setCanvas] = useState<ProjectCanvas>();
  const [error, setError] = useState("");
  const [, setTelemetryView] = useQueryState("telemetry", telemetryViewParser);
  const [, setLogState] = useQueryStates(logQueryParsers);
  const scope = useMemo<Exclude<MetricScope, { kind: "service" }>>(
    () => ({ kind: "project", projectID }),
    [projectID]
  );
  const serviceNames = useMemo(
    () =>
      new Map(
        (canvas?.resources ?? [])
          .filter((resource) => resource.kind === "service")
          .map((service) => [service.id, service.name])
      ),
    [canvas?.resources]
  );
  const serviceName = useCallback(
    (serviceID: string) => serviceNames.get(serviceID),
    [serviceNames]
  );

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setCanvas(await fetchProjectCanvas(projectID, controller.signal));
        setError("");
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load project telemetry"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID]);

  const openTraceLogs = useCallback(
    (traceID: string, spanID?: string) => {
      void Promise.all([
        setLogState({ logSpan: spanID ?? null, logTrace: traceID }),
        setTelemetryView("logs", { history: "push" }),
      ]);
    },
    [setLogState, setTelemetryView]
  );
  const openIssue = (issue: ScopedIssue) => {
    const query = new URLSearchParams({
      errorIssue: issue.id,
      telemetry: "errors",
    });
    void navigate(
      `/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(issue.serviceId)}/telemetry?${query.toString()}`
    );
  };

  if (new URLSearchParams(location.search).get("telemetry") === "analytics") {
    const params = new URLSearchParams(location.search);
    params.delete("telemetry");
    const suffix = params.toString();
    return (
      <Navigate
        replace
        to={`/projects/${encodeURIComponent(projectID)}/analytics${suffix ? `?${suffix}` : ""}`}
      />
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      {error ? (
        <p className="shrink-0 border-b border-destructive/35 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      {!canvas && !error ? (
        <div className="grid min-h-0 flex-1 place-items-center text-[10px] text-muted-foreground">
          <span className="flex items-center gap-2">
            <LoaderCircle className="size-3 animate-spin" /> Opening project
            telemetry
          </span>
        </div>
      ) : (
        <div className="min-h-0 flex-1">
          <TelemetryWorkspace
            views={{
              ai: <AiOverviewView scope={scope} />,
              errors: (
                <ScopedErrors
                  onOpenIssue={openIssue}
                  scope={scope}
                  serviceName={serviceName}
                />
              ),
              logs: (
                <ScopedTelemetryLogs scope={scope} serviceName={serviceName} />
              ),
              metrics: <ProjectUsage projectID={projectID} />,
              traces: (
                <TelemetryTraces
                  onOpenLogs={openTraceLogs}
                  scope={scope}
                  serviceName={serviceName}
                />
              ),
            }}
          />
        </div>
      )}
    </div>
  );
};
