import { AlertTriangle, FileClock, RefreshCw, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { fetchProjectCanvas, parseLogStreamMessage } from "@/api";
import type { LogWindow, Project, ProjectCanvas } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { applyLogStreamMessage, serviceLogSocketURL } from "@/log-stream";

const shortID = (value: string) => value.slice(0, 8);
const logWindowLimit = 500;

export const LogsPage = ({ projects }: { projects: Project[] }) => {
  const [selectedProjectID, setSelectedProjectID] = useState("");
  const [serviceID, setServiceID] = useState("");
  const [canvas, setCanvas] = useState<ProjectCanvas>();
  const [contains, setContains] = useState("");
  const [deploymentID, setDeploymentID] = useState("");
  const [window, setWindow] = useState<LogWindow>();
  const [loadingCanvas, setLoadingCanvas] = useState(false);
  const [loadingLogs, setLoadingLogs] = useState(false);
  const [streamRevision, setStreamRevision] = useState(0);
  const [streamStatus, setStreamStatus] = useState<
    "connected" | "connecting" | "disconnected"
  >("disconnected");
  const [appliedContains, setAppliedContains] = useState("");
  const [appliedDeploymentID, setAppliedDeploymentID] = useState("");
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
        setStreamStatus(firstService ? "connecting" : "disconnected");
        setLoadingLogs(Boolean(firstService));
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

  useEffect(() => {
    if (!projectID || !serviceID) {
      return;
    }
    let disposed = false;
    const socket = new WebSocket(
      serviceLogSocketURL(projectID, serviceID, {
        contains: appliedContains || undefined,
        deploymentId: appliedDeploymentID || undefined,
        limit: logWindowLimit,
      })
    );
    socket.addEventListener("open", () => {
      if (!disposed) {
        setStreamStatus("connected");
        setError(undefined);
      }
    });
    socket.addEventListener("message", (event) => {
      if (disposed || typeof event.data !== "string") {
        return;
      }
      try {
        const message = parseLogStreamMessage(JSON.parse(event.data));
        setWindow((current) =>
          applyLogStreamMessage(current, message, logWindowLimit)
        );
        setLoadingLogs(false);
        setError(undefined);
      } catch (messageError) {
        setError(
          messageError instanceof Error
            ? messageError.message
            : "Invalid log stream message"
        );
        socket.close(1003, "invalid log stream message");
      }
    });
    socket.addEventListener("close", () => {
      if (!disposed) {
        setStreamStatus("disconnected");
        setLoadingLogs(false);
        setError((current) => current ?? "Live log stream disconnected");
      }
    });
    socket.addEventListener("error", () => {
      if (!disposed) {
        setStreamStatus("disconnected");
      }
    });
    return () => {
      disposed = true;
      socket.close(1000, "log view changed");
    };
  }, [
    appliedContains,
    appliedDeploymentID,
    projectID,
    serviceID,
    streamRevision,
  ]);

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
              setAppliedContains("");
              setAppliedDeploymentID("");
              setLoadingLogs(true);
              setStreamStatus("connecting");
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
              setAppliedContains("");
              setAppliedDeploymentID("");
              setLoadingLogs(true);
              setStreamStatus("connecting");
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
            setAppliedContains(contains.trim());
            setAppliedDeploymentID(deploymentID.trim());
            setWindow(undefined);
            setLoadingLogs(true);
            setStreamStatus("connecting");
            setStreamRevision((current) => current + 1);
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
            Apply
          </Button>
        </form>
      </section>

      <section className="grid grid-cols-3 border-b border-border text-[10px] text-muted-foreground">
        <div className="border-r border-border px-5 py-3">
          Live window{" "}
          <span className="ml-1 text-foreground">
            {window?.records.length ?? 0} records
          </span>
        </div>
        <div className="border-r border-border px-5 py-3">
          Stream <span className="ml-1 text-foreground">{streamStatus}</span>
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
              className="log-record grid border-b border-border/70 md:grid-cols-[190px_72px_150px_minmax(0,1fr)]"
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
