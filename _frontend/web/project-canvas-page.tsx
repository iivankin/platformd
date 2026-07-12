import {
  Box,
  Database,
  HardDrive,
  Plus,
  Server,
  Waypoints,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type { ComponentType } from "react";
import { useParams } from "react-router";

import { fetchProjectCanvas } from "@/api";
import type { ProjectCanvas } from "@/api";
import { Button } from "@/components/ui/button";

const canvasWidth = 1200;
const nodeWidth = 240;
const nodeHeight = 92;
const columnGap = 320;
const rowGap = 168;
const startX = 72;
const startY = 72;

const resourceKinds: Record<
  ProjectCanvas["resources"][number]["kind"],
  { icon: ComponentType<{ className?: string }>; label: string }
> = {
  object_store: { icon: HardDrive, label: "Object storage" },
  postgres: { icon: Database, label: "PostgreSQL" },
  redis: { icon: Box, label: "Redis" },
  service: { icon: Server, label: "Service" },
};

interface Position {
  x: number;
  y: number;
}

const layoutResources = (canvas: ProjectCanvas) =>
  new Map<string, Position>(
    canvas.resources.map((resource, index) => [
      resource.id,
      {
        x: startX + (index % 3) * columnGap,
        y: startY + Math.floor(index / 3) * rowGap,
      },
    ])
  );

const resourceDetail = (resource: ProjectCanvas["resources"][number]) =>
  resource.imageReference ?? resource.bucketName ?? resource.internalHostname;

export const ProjectCanvasPage = () => {
  const { projectID = "" } = useParams();
  const [canvas, setCanvas] = useState<ProjectCanvas | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setCanvas(await fetchProjectCanvas(projectID, controller.signal));
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
            : "Unable to load project canvas"
        );
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID]);

  const positions = useMemo(
    () => (canvas ? layoutResources(canvas) : new Map<string, Position>()),
    [canvas]
  );
  const rows = canvas ? Math.ceil(canvas.resources.length / 3) : 1;
  const canvasHeight = Math.max(560, startY * 2 + rows * rowGap);

  return (
    <div className="enter-row flex h-full min-h-0 flex-col">
      <section className="flex min-h-14 shrink-0 items-center justify-between gap-4 border-b border-border px-5 py-3">
        <div className="min-w-0">
          <p className="truncate text-xs font-medium">
            {canvas?.project.name ?? "Project"}
          </p>
          <p className="mt-1 truncate text-[10px] text-muted-foreground">
            {canvas ? `${canvas.project.name}.internal` : "Loading namespace"}
          </p>
        </div>
        <Button size="sm">
          <Plus />
          New resource
        </Button>
      </section>

      {error ? (
        <section className="shrink-0 border-b border-destructive/30 bg-destructive/5 px-5 py-4 text-xs text-destructive">
          {error}
        </section>
      ) : null}

      <section className="project-canvas min-h-0 flex-1 overflow-auto">
        <div
          className="relative"
          style={{ height: canvasHeight, minWidth: canvasWidth }}
        >
          {canvas && canvas.resources.length === 0 ? (
            <div className="absolute inset-0 grid place-items-center text-center">
              <div className="max-w-sm">
                <Waypoints className="mx-auto size-7 text-muted-foreground" />
                <h2 className="mt-5 text-sm font-medium">
                  Empty project canvas
                </h2>
                <p className="mt-2 text-xs leading-5 text-muted-foreground">
                  Add a service, PostgreSQL, Redis, or object store. Connections
                  will appear automatically from service environment values.
                </p>
              </div>
            </div>
          ) : null}

          {canvas && canvas.connections.length > 0 ? (
            <svg
              aria-label="Resource connections"
              className="pointer-events-none absolute inset-0"
              height={canvasHeight}
              viewBox={`0 0 ${canvasWidth} ${canvasHeight}`}
              width={canvasWidth}
            >
              {canvas.connections.map((connection) => {
                const source = positions.get(connection.sourceId);
                const target = positions.get(connection.targetId);
                if (!(source && target)) {
                  return null;
                }
                const sourceX = source.x + nodeWidth;
                const sourceY = source.y + nodeHeight / 2;
                const targetX = target.x;
                const targetY = target.y + nodeHeight / 2;
                const controlOffset = Math.max(
                  48,
                  Math.abs(targetX - sourceX) / 2
                );
                const path = `M ${sourceX} ${sourceY} C ${sourceX + controlOffset} ${sourceY}, ${targetX - controlOffset} ${targetY}, ${targetX} ${targetY}`;
                return (
                  <g key={`${connection.sourceId}:${connection.targetId}`}>
                    <path
                      className="fill-none stroke-border"
                      d={path}
                      strokeWidth="1.5"
                    />
                    <circle
                      className="fill-muted-foreground"
                      cx={targetX}
                      cy={targetY}
                      r="2.5"
                    />
                    <title>{connection.environmentNames.join(", ")}</title>
                  </g>
                );
              })}
            </svg>
          ) : null}

          {canvas?.resources.map((resource) => {
            const position = positions.get(resource.id);
            const kind = resourceKinds[resource.kind];
            const Icon = kind.icon;
            if (!position) {
              return null;
            }
            return (
              <article
                className="absolute border border-border bg-background shadow-[0_1px_2px_oklch(0_0_0/6%)]"
                key={resource.id}
                style={{
                  height: nodeHeight,
                  left: position.x,
                  top: position.y,
                  width: nodeWidth,
                }}
              >
                <div className="flex h-9 items-center border-b border-border px-3">
                  <Icon className="size-3.5 text-muted-foreground" />
                  <span className="ml-2 min-w-0 flex-1 truncate text-xs font-medium">
                    {resource.name}
                  </span>
                  <span
                    className={
                      resource.enabled
                        ? "size-1.5 bg-emerald-500"
                        : "size-1.5 bg-muted-foreground"
                    }
                    title={resource.enabled ? "Enabled" : "Disabled"}
                  />
                </div>
                <div className="px-3 py-2.5">
                  <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
                    {kind.label}
                  </p>
                  <p className="mt-1.5 truncate text-[9px] text-muted-foreground">
                    {resourceDetail(resource)}
                  </p>
                </div>
              </article>
            );
          })}
        </div>
      </section>
    </div>
  );
};
