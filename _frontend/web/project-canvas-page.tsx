import { ContextMenu } from "@base-ui/react/context-menu";
import {
  Background,
  BackgroundVariant,
  Controls,
  ReactFlow,
  useEdgesState,
  useNodesState,
} from "@xyflow/react";
import { Plus, Waypoints } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { fetchProjectCanvas } from "@/api";
import type { Project, ProjectCanvas } from "@/api";
import { Button } from "@/components/ui/button";
import { NetworkGatewayDraftPage } from "@/network-gateway-draft-page";
import {
  applyPendingResource,
  mergePendingCanvasResources,
} from "@/pending-resource-creation";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import { applyProjectOperations } from "@/project-apply";
import type { ProjectApplyOperation } from "@/project-apply";
import { projectCanvasForDemoPreset } from "@/project-canvas-demo";
import type { DemoCanvasPreset } from "@/project-canvas-demo";
import { ProjectCanvasDemoSwitcher } from "@/project-canvas-demo-switcher";
import { ProjectChangeBar } from "@/project-change-bar";
import { useProjectChanges } from "@/project-changes";
import { ProjectCreateOverlays } from "@/project-create-overlays";
import type { CreateKind } from "@/project-create-overlays";
import { ProjectDeploymentPage } from "@/project-deployment-page";
import { mergeResourceNodeData, projectFlowElements } from "@/project-flow";
import type {
  ResourceFlowEdge,
  ResourceFlowNode,
  ResourceNodeOverlay,
} from "@/project-flow";
import { ProjectResourcePage } from "@/project-resource-page";
import { resourcePath } from "@/project-resource-path";
import { ProjectSettingsDialog } from "@/project-settings-dialog";
import { ResourceConnectionEdge } from "@/resource-connection-edge";
import { resourceCreateOptions } from "@/resource-create-panel";
import { ResourceDraftPage } from "@/resource-draft-page";
import { ResourceNode } from "@/resource-node";
import { ServiceDraftPage } from "@/service-draft-page";
import { applyServiceSettings } from "@/service-settings-apply";
import { serviceSettingsChangeDetails } from "@/service-settings-model";
import type { PendingServiceSettings } from "@/service-settings-model";
import { forgetLastProject } from "@/use-last-project";

const nodeTypes = { resource: ResourceNode };
const edgeTypes = { resourceConnection: ResourceConnectionEdge };
const emptyNodes: ResourceFlowNode[] = [];
const emptyEdges: ResourceFlowEdge[] = [];
const statusRefreshMilliseconds = 5000;

interface CanvasApplyOperation extends ProjectApplyOperation {
  type: "resource" | "service";
}

const resourceOverlays = (
  changes: Record<string, PendingServiceSettings>
): ReadonlyMap<string, ResourceNodeOverlay> =>
  new Map(
    Object.entries(changes).map(([serviceID, change]) => {
      const mountPaths = new Map(
        change.draft.volumeMounts.map((mount) => [
          mount.volumeId,
          mount.containerPath,
        ])
      );
      return [
        serviceID,
        {
          pendingChangeCount: serviceSettingsChangeDetails(change).length,
          volumes: change.draft.volumes.map((volume) => ({
            containerPath: mountPaths.get(volume.id),
            id: volume.id,
            name: volume.name,
          })),
        },
      ];
    })
  );

const canvasLayoutSignature = (
  canvas: ProjectCanvas,
  overlays: ReadonlyMap<string, ResourceNodeOverlay>
): string =>
  JSON.stringify({
    connections: canvas.connections.map(({ sourceId, targetId }) => [
      sourceId,
      targetId,
    ]),
    resources: canvas.resources.map((resource) => {
      const overlay = overlays.get(resource.id);
      return [
        resource.id,
        overlay?.pendingChangeCount ??
          (resource.id.startsWith("draft:") ? 1 : 0),
        (overlay?.volumes ?? resource.volumes).map((volume) => volume.id),
      ];
    }),
  });

const EmptyCanvas = ({ visible }: { visible: boolean }) => {
  if (!visible) {
    return null;
  }
  return (
    <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center text-center">
      <div className="max-w-sm">
        <Waypoints className="mx-auto size-7 text-muted-foreground" />
        <h2 className="mt-5 text-sm font-medium">Empty project canvas</h2>
        <p className="mt-2 text-xs leading-5 text-muted-foreground">
          Add a service, database, storage, or network gateway. Connections
          appear from explicit references configured in service Variables.
        </p>
      </div>
    </div>
  );
};

const ProjectRouteOverlay = ({
  canvas,
  canvasWithDrafts,
  deploymentID,
  onDraftChange,
  projectID,
  resourceID,
  routedDraft,
  view,
}: {
  canvas: ProjectCanvas | null;
  canvasWithDrafts: ProjectCanvas | null;
  deploymentID: string;
  onDraftChange: (draft: PendingResourceCreation) => void;
  projectID: string;
  resourceID: string;
  routedDraft?: PendingResourceCreation;
  view: string;
}) => {
  if (deploymentID) {
    return <ProjectDeploymentPage canvas={canvas} />;
  }
  if (routedDraft?.kind === "service") {
    return (
      <ServiceDraftPage
        draft={routedDraft}
        onChange={onDraftChange}
        projectID={projectID}
        projectName={canvas?.project.name ?? ""}
        view={view}
      />
    );
  }
  if (routedDraft?.kind === "network_gateway") {
    return (
      <NetworkGatewayDraftPage
        draft={routedDraft}
        onChange={onDraftChange}
        projectID={projectID}
        projectName={canvas?.project.name ?? ""}
        resources={canvasWithDrafts?.resources ?? []}
        view={view}
      />
    );
  }
  if (routedDraft) {
    return (
      <ResourceDraftPage
        draft={routedDraft}
        onChange={onDraftChange}
        projectID={projectID}
        projectName={canvas?.project.name ?? ""}
        view={view}
      />
    );
  }
  return resourceID ? <ProjectResourcePage /> : null;
};

export const ProjectCanvasPage = ({
  isDemo,
  onProjectDeleted,
  onProjectUpdated,
}: {
  isDemo: boolean;
  onProjectDeleted: (projectID: string) => void;
  onProjectUpdated: (project: Project) => void;
}) => {
  const navigate = useNavigate();
  const {
    deploymentID = "",
    projectID = "",
    resourceID = "",
    view = "",
  } = useParams();
  const [canvas, setCanvas] = useState<ProjectCanvas | null>(null);
  const [canvasError, setCanvasError] = useState<string | null>(null);
  const [createKind, setCreateKind] = useState<CreateKind>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [applyingChanges, setApplyingChanges] = useState(false);
  const [applyError, setApplyError] = useState<string>();
  const [demoCanvasPreset, setDemoCanvasPreset] =
    useState<DemoCanvasPreset>("default");
  const [layoutRevision, setLayoutRevision] = useState(0);
  const layoutSignatureRef = useRef("");
  const { resourceDrafts, serviceChanges, setResourceDraft, setServiceChange } =
    useProjectChanges(projectID);
  const [applyingResourceDraftIDs, setApplyingResourceDraftIDs] = useState<
    ReadonlySet<string>
  >(new Set());
  const [nodes, setNodes, onNodesChange] =
    useNodesState<ResourceFlowNode>(emptyNodes);
  const [edges, setEdges, onEdgesChange] =
    useEdgesState<ResourceFlowEdge>(emptyEdges);
  const pendingResources = useMemo(
    () =>
      Object.values(resourceDrafts).toSorted((left, right) =>
        left.input.name.localeCompare(right.input.name)
      ),
    [resourceDrafts]
  );
  const canvasWithDrafts = useMemo<ProjectCanvas | null>(() => {
    if (!canvas) {
      return null;
    }
    return {
      ...canvas,
      resources: mergePendingCanvasResources(
        canvas.resources,
        pendingResources,
        canvas.project.name,
        applyingResourceDraftIDs
      ),
    };
  }, [applyingResourceDraftIDs, canvas, pendingResources]);
  const displayedCanvas = useMemo(
    () =>
      canvasWithDrafts
        ? projectCanvasForDemoPreset(
            canvasWithDrafts,
            isDemo ? demoCanvasPreset : "default"
          )
        : null,
    [canvasWithDrafts, demoCanvasPreset, isDemo]
  );
  const overlays = useMemo(
    () => resourceOverlays(serviceChanges),
    [serviceChanges]
  );
  const isCanvasEmpty = displayedCanvas?.resources.length === 0;
  const pageError = canvasError;
  const pendingServices = useMemo(
    () =>
      Object.values(serviceChanges).toSorted((left, right) =>
        left.serviceName.localeCompare(right.serviceName)
      ),
    [serviceChanges]
  );
  const routedDraft = resourceDrafts[resourceID];

  useEffect(() => {
    const controller = new AbortController();
    let refreshTimer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const loaded = await fetchProjectCanvas(projectID, controller.signal);
        setCanvas(loaded);
        setCanvasError(null);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setCanvasError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load project canvas"
        );
      } finally {
        if (!controller.signal.aborted) {
          refreshTimer = setTimeout(
            () => void load(),
            statusRefreshMilliseconds
          );
        }
      }
    };
    void load();
    return () => {
      controller.abort();
      if (refreshTimer) {
        clearTimeout(refreshTimer);
      }
    };
  }, [projectID, refreshVersion, setEdges, setNodes]);

  useEffect(() => {
    if (!displayedCanvas) {
      return;
    }
    let cancelled = false;
    const signature = canvasLayoutSignature(displayedCanvas, overlays);
    const layout = async () => {
      try {
        const flow = await projectFlowElements(displayedCanvas, overlays);
        if (cancelled) {
          return;
        }
        setNodes((current) => mergeResourceNodeData(current, flow.nodes));
        setEdges(flow.edges);
        if (layoutSignatureRef.current !== signature) {
          layoutSignatureRef.current = signature;
          setLayoutRevision((revision) => revision + 1);
        }
      } catch (layoutError) {
        if (!cancelled) {
          setCanvasError(
            layoutError instanceof Error
              ? layoutError.message
              : "Unable to lay out project canvas"
          );
        }
      }
    };
    void layout();
    return () => {
      cancelled = true;
    };
  }, [displayedCanvas, overlays, setEdges, setNodes]);

  const changeDemoCanvasPreset = (preset: DemoCanvasPreset) => {
    setDemoCanvasPreset(preset);
  };

  const applyChanges = async () => {
    if (
      applyingChanges ||
      (pendingServices.length === 0 && pendingResources.length === 0)
    ) {
      return;
    }
    if (!canvas) {
      setApplyError("Project resources are still loading");
      return;
    }
    setApplyingChanges(true);
    setApplyError(undefined);
    const resourceDraftIDs = new Set(pendingResources.map((draft) => draft.id));
    setApplyingResourceDraftIDs(resourceDraftIDs);
    const operations: CanvasApplyOperation[] = [
      ...pendingServices.map((change) => ({
        environment: change.environment,
        id: change.serviceID,
        label: change.serviceName,
        resourceName: change.serviceName,
        run: () => applyServiceSettings(projectID, change),
        type: "service" as const,
      })),
      ...pendingResources.map((draft) => ({
        environment:
          draft.kind === "service" ? draft.input.environment : undefined,
        id: draft.id,
        label: draft.input.name,
        resourceName: draft.input.name,
        run: () => applyPendingResource(projectID, draft),
        type: "resource" as const,
      })),
    ];
    try {
      const results = await applyProjectOperations(
        operations,
        new Set(canvas.resources.map((resource) => resource.name))
      );
      let firstError: string | undefined;
      let blocked = 0;
      let applied = false;
      for (const result of results) {
        const { operation } = result;
        if (result.status === "fulfilled") {
          applied = true;
          if (operation.type === "service") {
            setServiceChange(operation.id);
          } else {
            setResourceDraft(operation.id);
          }
          continue;
        }
        if (result.status === "blocked") {
          blocked += 1;
        } else if (!firstError) {
          const message =
            result.reason instanceof Error
              ? result.reason.message
              : "Unable to apply project change";
          firstError = `${operation.label}: ${message}`;
        }
      }
      if (applied) {
        setRefreshVersion((value) => value + 1);
      }
      setApplyError(
        firstError && blocked > 0
          ? `${firstError} · ${blocked} dependent ${blocked === 1 ? "change was" : "changes were"} not applied`
          : firstError
      );
    } catch (error) {
      setApplyError(
        error instanceof Error
          ? error.message
          : "Unable to plan project changes"
      );
    } finally {
      setApplyingResourceDraftIDs(new Set());
      setApplyingChanges(false);
    }
  };

  const discardPendingChanges = () => {
    for (const change of pendingServices) {
      setServiceChange(change.serviceID);
    }
    for (const draft of pendingResources) {
      setResourceDraft(draft.id);
    }
    setApplyError(undefined);
  };

  const handleProjectDeleted = (deletedProjectID: string) => {
    discardPendingChanges();
    forgetLastProject(deletedProjectID);
    onProjectDeleted(deletedProjectID);
    void navigate("/projects", { replace: true });
  };

  const handleProjectUpdated = (project: Project) => {
    onProjectUpdated(project);
    setCanvas((current) =>
      current
        ? { ...current, project: { ...current.project, ...project } }
        : current
    );
  };

  return (
    <div className="flex h-full min-h-0 animate-in flex-col duration-200 fade-in slide-in-from-bottom-1">
      <section className="flex min-h-12 shrink-0 items-center gap-4 border-b border-border px-5 py-2.5">
        <div className="flex min-w-0 items-center gap-2.5">
          <p className="truncate text-xs font-medium">
            {canvas?.project.name ?? "Project"}
          </p>
          {canvas ? (
            <ProjectSettingsDialog
              onDeleted={handleProjectDeleted}
              onUpdated={handleProjectUpdated}
              project={canvas.project}
            />
          ) : null}
        </div>
      </section>

      {pageError ? (
        <section className="shrink-0 border-b border-destructive/30 bg-destructive/5 px-5 py-4 text-xs text-destructive">
          {pageError}
        </section>
      ) : null}

      <section className="relative min-h-0 flex-1 bg-background">
        {isDemo ? (
          <ProjectCanvasDemoSwitcher
            onChange={changeDemoCanvasPreset}
            value={demoCanvasPreset}
          />
        ) : null}
        <Button
          className="absolute top-4 right-4 z-10 shadow-sm max-sm:top-auto max-sm:bottom-4 sm:right-5"
          onClick={() => setCreateKind("picker")}
          size="sm"
        >
          <Plus />
          <span className="hidden sm:inline">New resource</span>
          <span className="sm:hidden">New</span>
        </Button>
        <ProjectChangeBar
          applying={applyingChanges}
          changes={pendingServices}
          error={applyError}
          onApply={() => void applyChanges()}
          onDiscard={discardPendingChanges}
          resourceDrafts={pendingResources}
        />
        <ProjectCreateOverlays
          kind={createKind}
          onClose={() => {
            setCreateKind(null);
          }}
          onDrafted={(draft) => {
            setResourceDraft(draft.id, draft);
            setCreateKind(null);
          }}
          onSelect={(kind) => {
            setCreateKind(kind);
          }}
          projectID={projectID}
          resources={canvas?.resources ?? []}
        />
        <EmptyCanvas visible={isCanvasEmpty === true} />
        <ContextMenu.Root>
          <ContextMenu.Trigger className="absolute inset-0">
            <ReactFlow<ResourceFlowNode, ResourceFlowEdge>
              edgeTypes={edgeTypes}
              edges={edges}
              edgesFocusable={false}
              edgesReconnectable={false}
              fitView
              fitViewOptions={{ maxZoom: 1, padding: 0.24 }}
              key={`${projectID}:${layoutRevision}`}
              maxZoom={1.75}
              minZoom={0.25}
              nodeTypes={nodeTypes}
              nodes={nodes}
              nodesConnectable={false}
              nodesDraggable
              onEdgesChange={onEdgesChange}
              onNodeClick={(_event, node) => {
                if (isDemo && demoCanvasPreset !== "default") {
                  return;
                }
                const draft = resourceDrafts[node.id];
                if (draft) {
                  const kind =
                    draft.kind === "storage" ? "object_store" : draft.kind;
                  void navigate(
                    resourcePath(projectID, draft.id, kind, "variables")
                  );
                  return;
                }
                void navigate(resourcePath(projectID, node.id, node.data.kind));
              }}
              onNodeContextMenu={(event) => event.stopPropagation()}
              onNodesChange={onNodesChange}
              onlyRenderVisibleElements
              panOnScroll
              proOptions={{ hideAttribution: true }}
              selectNodesOnDrag={false}
            >
              <Background
                color="var(--border)"
                gap={16}
                size={1}
                variant={BackgroundVariant.Dots}
              />
              <Controls
                aria-label="Canvas navigation"
                fitViewOptions={{ maxZoom: 1, padding: 0.24 }}
                position="bottom-right"
                showInteractive={false}
              />
            </ReactFlow>
          </ContextMenu.Trigger>
          <ContextMenu.Portal>
            <ContextMenu.Positioner className="z-50">
              <ContextMenu.Popup className="min-w-52 border border-border bg-popover p-1 text-[10px] text-popover-foreground shadow-lg">
                <ContextMenu.Group>
                  <ContextMenu.GroupLabel className="px-2.5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                    Create resource
                  </ContextMenu.GroupLabel>
                  <ContextMenu.Separator className="mb-1 h-px bg-border" />
                  {resourceCreateOptions.map((option) => {
                    const Icon = option.icon;
                    return (
                      <ContextMenu.Item
                        className="flex cursor-default items-center gap-2 px-2.5 py-2 outline-none data-[highlighted]:bg-muted"
                        key={option.kind}
                        onClick={() => setCreateKind(option.kind)}
                      >
                        <Icon className="size-3.5 text-muted-foreground" />
                        {option.label}
                      </ContextMenu.Item>
                    );
                  })}
                </ContextMenu.Group>
              </ContextMenu.Popup>
            </ContextMenu.Positioner>
          </ContextMenu.Portal>
        </ContextMenu.Root>
        {/* Applying settings clears staged state. Remount the open overlay so it
            fetches the committed service instead of rendering its stale baseline. */}
        <ProjectRouteOverlay
          canvas={canvas}
          canvasWithDrafts={canvasWithDrafts}
          deploymentID={deploymentID}
          key={refreshVersion}
          onDraftChange={(draft) => setResourceDraft(draft.id, draft)}
          projectID={projectID}
          resourceID={resourceID}
          routedDraft={routedDraft}
          view={view}
        />
      </section>
    </div>
  );
};
