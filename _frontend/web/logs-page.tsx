import { AlertTriangle, FileClock, RefreshCw, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { fetchProjectCanvas, fetchServiceLogs } from "@/api";
import type { LogWindow, Project, ProjectCanvas } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const shortID = (value: string) => value.slice(0, 8);

export const LogsPage = ({ projects }: { projects: Project[] }) => {
  const [selectedProjectID, setSelectedProjectID] = useState("");
  const [serviceID, setServiceID] = useState("");
  const [canvas, setCanvas] = useState<ProjectCanvas>();
  const [contains, setContains] = useState("");
  const [deploymentID, setDeploymentID] = useState("");
  const [window, setWindow] = useState<LogWindow>();
  const [loadingCanvas, setLoadingCanvas] = useState(false);
  const [loadingLogs, setLoadingLogs] = useState(false);
  const [error, setError] = useState<string>();

  const projectID = selectedProjectID || projects[0]?.id || "";

  useEffect(() => {
    if (!projectID) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      setLoadingCanvas(true);
      try {
        const loaded = await fetchProjectCanvas(projectID, controller.signal);
        setCanvas(loaded);
        const firstService = loaded.resources.find(
          (resource) => resource.kind === "service"
        );
        setServiceID((current) =>
          loaded.resources.some(
            (resource) => resource.kind === "service" && resource.id === current
          )
            ? current
            : (firstService?.id ?? "")
        );
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load project services"
          );
        }
      } finally {
        setLoadingCanvas(false);
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID]);

  const services = useMemo(
    () =>
      canvas?.resources.filter((resource) => resource.kind === "service") ?? [],
    [canvas]
  );

  const loadLogs = async () => {
    if (!projectID || !serviceID || loadingLogs) {
      return;
    }
    setLoadingLogs(true);
    try {
      setWindow(
        await fetchServiceLogs(projectID, serviceID, {
          contains: contains.trim() || undefined,
          deploymentId: deploymentID.trim() || undefined,
          limit: 500,
        })
      );
      setError(undefined);
    } catch (loadError) {
      setError(
        loadError instanceof Error ? loadError.message : "Unable to read logs"
      );
    } finally {
      setLoadingLogs(false);
    }
  };

  useEffect(() => {
    if (!projectID || !serviceID) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      setLoadingLogs(true);
      try {
        setWindow(
          await fetchServiceLogs(
            projectID,
            serviceID,
            { limit: 500 },
            controller.signal
          )
        );
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to read logs"
          );
        }
      } finally {
        setLoadingLogs(false);
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, serviceID]);

  return (
    <div className="enter-row min-h-full">
      <section className="flex flex-wrap items-end gap-3 border-b border-border px-5 py-4">
        <label className="grid min-w-52 gap-1.5 text-[10px] text-muted-foreground">
          Project
          <select
            className="h-8 border border-input bg-background px-2.5 text-xs text-foreground outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/30"
            disabled={projects.length === 0}
            onChange={(event) => {
              setSelectedProjectID(event.target.value);
              setServiceID("");
              setDeploymentID("");
              setCanvas(undefined);
              setWindow(undefined);
            }}
            value={projectID}
          >
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </label>
        <label className="grid min-w-52 gap-1.5 text-[10px] text-muted-foreground">
          Service
          <select
            className="h-8 border border-input bg-background px-2.5 text-xs text-foreground outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/30"
            disabled={loadingCanvas || services.length === 0}
            onChange={(event) => {
              setServiceID(event.target.value);
              setDeploymentID("");
              setWindow(undefined);
            }}
            value={serviceID}
          >
            {services.length === 0 ? (
              <option value="">No services</option>
            ) : null}
            {services.map((service) => (
              <option key={service.id} value={service.id}>
                {service.name}
              </option>
            ))}
          </select>
        </label>
        <label
          className="grid min-w-48 gap-1.5 text-[10px] text-muted-foreground"
          htmlFor="log-deployment"
        >
          Deployment ID
          <Input
            className="h-8 font-mono text-[10px]"
            id="log-deployment"
            onChange={(event) => setDeploymentID(event.target.value)}
            placeholder="All deployments"
            value={deploymentID}
          />
        </label>
        <form
          className="flex min-w-64 flex-1 items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            void loadLogs();
          }}
        >
          <label
            className="grid flex-1 gap-1.5 text-[10px] text-muted-foreground"
            htmlFor="log-contains"
          >
            Contains
            <span className="relative">
              <Search className="pointer-events-none absolute top-2 left-2.5 size-3.5" />
              <Input
                className="h-8 pl-8 text-xs"
                id="log-contains"
                maxLength={256}
                onChange={(event) => setContains(event.target.value)}
                placeholder="Filter the bounded window"
                value={contains}
              />
            </span>
          </label>
          <Button
            disabled={!serviceID || loadingLogs}
            size="sm"
            type="submit"
            variant="outline"
          >
            <RefreshCw className={cn(loadingLogs && "animate-spin")} />
            Refresh
          </Button>
        </form>
      </section>

      <section className="grid grid-cols-3 border-b border-border text-[10px] text-muted-foreground">
        <div className="border-r border-border px-5 py-3">
          Window{" "}
          <span className="ml-1 text-foreground">
            {window?.records.length ?? 0} records
          </span>
        </div>
        <div className="border-r border-border px-5 py-3">
          Source <span className="ml-1 text-foreground">conmon k8s-file</span>
        </div>
        <div className="px-5 py-3">
          Limit <span className="ml-1 text-foreground">500 recent matches</span>
        </div>
      </section>

      {error ? (
        <section className="flex items-center gap-2 border-b border-destructive/40 bg-destructive/5 px-5 py-3 text-xs text-destructive">
          <AlertTriangle className="size-4" />
          {error}
        </section>
      ) : null}
      {window?.truncated ? (
        <section className="flex items-center gap-2 border-b border-amber-500/30 bg-amber-500/5 px-5 py-2.5 text-[10px] text-amber-700 dark:text-amber-300">
          <AlertTriangle className="size-3.5" />
          Older records exist outside this bounded window.
        </section>
      ) : null}

      {window?.records.length ? (
        <section
          aria-label="Service log records"
          className="font-mono text-[11px] leading-5"
        >
          {window.records.map((record, index) => (
            <div
              className="grid border-b border-border/70 md:grid-cols-[190px_72px_150px_minmax(0,1fr)]"
              key={`${record.attemptId}-${record.timestamp}-${index}`}
            >
              <time className="px-3 py-2 text-muted-foreground md:border-r md:border-border/70">
                {new Date(record.timestamp).toLocaleString()}
              </time>
              <span
                className={cn(
                  "px-3 py-2 font-semibold md:border-r md:border-border/70",
                  record.stream === "stderr"
                    ? "text-rose-500"
                    : "text-cyan-600 dark:text-cyan-400"
                )}
              >
                {record.stream}
              </span>
              <span
                className="px-3 py-2 text-muted-foreground md:border-r md:border-border/70"
                title={`${record.deploymentId} / ${record.attemptId}`}
              >
                {shortID(record.deploymentId)} / {shortID(record.attemptId)}
              </span>
              <pre className="min-w-0 overflow-x-auto px-3 py-2 whitespace-pre-wrap text-foreground">
                {record.text}
                {record.partial ? (
                  <span className="ml-2 text-amber-500">[partial]</span>
                ) : null}
              </pre>
            </div>
          ))}
        </section>
      ) : (
        <section className="grid min-h-80 place-items-center border-b border-border px-8 py-16 text-center">
          <div className="max-w-sm">
            <FileClock className="mx-auto mb-5 size-6 text-muted-foreground" />
            <p className="text-xs font-medium">
              {serviceID ? "No matching records" : "Select a service"}
            </p>
            <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
              Logs are read from a bounded recent window and are never rendered
              as terminal or HTML content.
            </p>
          </div>
        </section>
      )}
    </div>
  );
};
