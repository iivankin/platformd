import { useQueryState, useQueryStates } from "nuqs";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router";

import { AiOverviewView } from "@/ai-overview";
import { fetchProjectCanvas } from "@/api";
import type { MetricScope, Project, ScopedIssue } from "@/api";
import { ScopedTelemetryLogs } from "@/resource-logs";
import { InstallationUsage } from "@/resource-usage";
import { ScopedErrors } from "@/scoped-errors";
import { TelemetryTraces } from "@/service-traces";
import { logQueryParsers, telemetryViewParser } from "@/telemetry-query-state";
import { TelemetryWorkspace } from "@/telemetry-workspace";

interface ServiceLocation {
  name: string;
  projectID: string;
}

export const GlobalTelemetryPage = ({ projects }: { projects: Project[] }) => {
  const navigate = useNavigate();
  const [, setTelemetryView] = useQueryState("telemetry", telemetryViewParser);
  const [, setLogState] = useQueryStates(logQueryParsers);
  const [services, setServices] = useState(new Map<string, ServiceLocation>());
  const scope = useMemo<Exclude<MetricScope, { kind: "service" }>>(
    () => ({ kind: "installation" }),
    []
  );

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      const canvases = await Promise.allSettled(
        projects.map((project) =>
          fetchProjectCanvas(project.id, controller.signal)
        )
      );
      if (controller.signal.aborted) {
        return;
      }
      const next = new Map<string, ServiceLocation>();
      for (const result of canvases) {
        if (result.status !== "fulfilled") {
          continue;
        }
        for (const resource of result.value.resources) {
          if (resource.kind === "service") {
            next.set(resource.id, {
              name: resource.name,
              projectID: result.value.project.id,
            });
          }
        }
      }
      setServices(next);
    };
    void load();
    return () => controller.abort();
  }, [projects]);

  const serviceName = useCallback(
    (serviceID: string) => services.get(serviceID)?.name,
    [services]
  );
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
    const projectID =
      issue.projectId ?? services.get(issue.serviceId)?.projectID;
    if (!projectID) {
      return;
    }
    const query = new URLSearchParams({
      errorIssue: issue.id,
      telemetry: "errors",
    });
    void navigate(
      `/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(issue.serviceId)}/telemetry?${query.toString()}`
    );
  };

  return (
    <div className="h-full min-h-0 animate-in duration-200 fade-in slide-in-from-bottom-1">
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
          logs: <ScopedTelemetryLogs scope={scope} serviceName={serviceName} />,
          metrics: <InstallationUsage />,
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
  );
};
