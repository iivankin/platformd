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

import { fetchProjectCanvas, fetchRegistrySettings } from "@/api";
import type { ProjectCanvas } from "@/api";
import { Button } from "@/components/ui/button";
import {
  applyPendingResource,
  pendingCanvasResource,
} from "@/pending-resource-creation";
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
import { ResourceDraftPage } from "@/resource-draft-page";
import { ResourceNode } from "@/resource-node";
import { ServiceDraftPage } from "@/service-draft-page";
import { applyServiceSettings } from "@/service-settings-apply";
import { serviceSettingsChangeDetails } from "@/service-settings-model";
import type { PendingServiceSettings } from "@/service-settings-model";

const nodeTypes = { resource: ResourceNode };
const emptyNodes: ResourceFlowNode[] = [];
const emptyEdges: ResourceFlowEdge[] = [];
const statusRefreshMilliseconds = 5000;

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
          Add a service, PostgreSQL, Redis, or object store. Connections appear
          from explicit references configured in service Variables.
        </p>
      </div>
    </div>
  );
};

export const ProjectCanvasPage = () => {
  const navigate = useNavigate();
  const {
    deploymentID = "",
    projectID = "",
    resourceID = "",
    view = "",
  } = useParams();
  const [canvas, setCanvas] = useState<ProjectCanvas | null>(null);
  const [canvasError, setCanvasError] = useState<string | null>(null);
  const [metadataError, setMetadataError] = useState<string | null>(null);
  const [embeddedRegistryHost, setEmbeddedRegistryHost] = useState("");
  const [createKind, setCreateKind] = useState<CreateKind>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [applyingChanges, setApplyingChanges] = useState(false);
  const [applyError, setApplyError] = useState<string>();
  const { resourceDrafts, serviceChanges, setResourceDraft, setServiceChange } =
    useProjectChanges(projectID);
  const serviceChangesRef = useRef(serviceChanges);
  const resourceDraftsRef = useRef(resourceDrafts);
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
      resources: [
        ...canvas.resources,
        ...pendingResources.map((draft) =>
          pendingCanvasResource(draft, canvas.project.name)
        ),
      ],
    };
  }, [canvas, pendingResources]);
  const isCanvasEmpty = canvasWithDrafts?.resources.length === 0;
  const error = canvasError ?? metadataError;
  const pendingServices = useMemo(
    () =>
      Object.values(serviceChanges).toSorted((left, right) =>
        left.serviceName.localeCompare(right.serviceName)
      ),
    [serviceChanges]
  );
  const routedDraft = resourceDrafts[resourceID];
  let resourceOverlay = null;
  if (deploymentID) {
    resourceOverlay = <ProjectDeploymentPage canvas={canvas} />;
  } else if (routedDraft?.kind === "service") {
    resourceOverlay = (
      <ServiceDraftPage
        draft={routedDraft}
        embeddedRegistryHost={embeddedRegistryHost}
        onChange={(draft) => setResourceDraft(draft.id, draft)}
        projectID={projectID}
        projectName={canvas?.project.name ?? ""}
        view={view}
      />
    );
  } else if (routedDraft) {
    resourceOverlay = (
      <ResourceDraftPage
        draft={routedDraft}
        onChange={(draft) => setResourceDraft(draft.id, draft)}
        projectID={projectID}
        projectName={canvas?.project.name ?? ""}
        view={view}
      />
    );
  } else if (resourceID) {
    resourceOverlay = <ProjectResourcePage />;
  }

  useEffect(() => {
    const controller = new AbortController();
    let refreshTimer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const loaded = await fetchProjectCanvas(projectID, controller.signal);
        const withDrafts = {
          ...loaded,
          resources: [
            ...loaded.resources,
            ...Object.values(resourceDraftsRef.current).map((draft) =>
              pendingCanvasResource(draft, loaded.project.name)
            ),
          ],
        };
        const flow = projectFlowElements(
          withDrafts,
          resourceOverlays(serviceChangesRef.current)
        );
        setCanvas(loaded);
        setNodes((current) => mergeResourceNodeData(current, flow.nodes));
        setEdges(flow.edges);
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
    const controller = new AbortController();
    const load = async () => {
      try {
        const registrySettings = await fetchRegistrySettings(controller.signal);
        setEmbeddedRegistryHost(registrySettings.hostname);
        setMetadataError(null);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setMetadataError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load project settings"
        );
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID, refreshVersion]);

  useEffect(() => {
    serviceChangesRef.current = serviceChanges;
    resourceDraftsRef.current = resourceDrafts;
    if (canvasWithDrafts) {
      const flow = projectFlowElements(
        canvasWithDrafts,
        resourceOverlays(serviceChanges)
      );
      setNodes((current) => mergeResourceNodeData(current, flow.nodes));
      setEdges(flow.edges);
    }
  }, [canvasWithDrafts, resourceDrafts, serviceChanges, setEdges, setNodes]);

  const applyChanges = async () => {
    if (
      applyingChanges ||
      (pendingServices.length === 0 && pendingResources.length === 0)
    ) {
      return;
    }
    setApplyingChanges(true);
    setApplyError(undefined);
    const operations = [
      ...pendingServices.map((change) => ({
        id: change.serviceID,
        label: change.serviceName,
        run: () => applyServiceSettings(projectID, change),
        type: "service" as const,
      })),
      ...pendingResources.map((draft) => ({
        id: draft.id,
        label: draft.input.name,
        run: () => applyPendingResource(projectID, draft),
        type: "resource" as const,
      })),
    ];
    const results = await Promise.allSettled(
      operations.map((operation) => operation.run())
    );
    let firstError: string | undefined;
    let applied = false;
    for (const [index, result] of results.entries()) {
      const operation = operations[index];
      if (!operation) {
        continue;
      }
      if (result.status === "fulfilled") {
        applied = true;
        if (operation.type === "service") {
          setServiceChange(operation.id);
        } else {
          setResourceDraft(operation.id);
        }
        continue;
      }
      if (!firstError) {
        const message =
          result.reason instanceof Error
            ? result.reason.message
            : "Unable to apply service settings";
        firstError = `${operation.label}: ${message}`;
      }
    }
    if (applied) {
      setRefreshVersion((value) => value + 1);
    }
    setApplyError(firstError);
    setApplyingChanges(false);
  };

  return (
    <div className="flex h-full min-h-0 animate-in flex-col duration-200 fade-in slide-in-from-bottom-1">
      <section className="flex min-h-14 shrink-0 items-center justify-between gap-4 border-b border-border px-5 py-3">
        <div className="min-w-0">
          <p className="truncate text-xs font-medium">
            {canvas?.project.name ?? "Project"}
          </p>
          <p className="mt-1 truncate text-[10px] text-muted-foreground">
            {canvas ? `${canvas.project.name}.internal` : "Loading namespace"}
          </p>
        </div>
        <Button onClick={() => setCreateKind("picker")} size="sm">
          <Plus />
          New resource
        </Button>
      </section>

      {error ? (
        <section className="shrink-0 border-b border-destructive/30 bg-destructive/5 px-5 py-4 text-xs text-destructive">
          {error}
        </section>
      ) : null}

      <section className="relative min-h-0 flex-1 bg-background">
        <ProjectChangeBar
          applying={applyingChanges}
          changes={pendingServices}
          error={applyError}
          onApply={() => void applyChanges()}
          onDiscard={() => {
            for (const change of pendingServices) {
              setServiceChange(change.serviceID);
            }
            for (const draft of pendingResources) {
              setResourceDraft(draft.id);
            }
            setApplyError(undefined);
          }}
          resourceDrafts={pendingResources}
        />
        <ProjectCreateOverlays
          embeddedRegistryHost={embeddedRegistryHost}
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
        />
        <EmptyCanvas visible={isCanvasEmpty === true} />
        <ReactFlow<ResourceFlowNode, ResourceFlowEdge>
          edges={edges}
          edgesFocusable={false}
          edgesReconnectable={false}
          elementsSelectable
          fitView
          fitViewOptions={{ maxZoom: 1, padding: 0.24 }}
          key={projectID}
          maxZoom={1.75}
          minZoom={0.25}
          nodeTypes={nodeTypes}
          nodes={nodes}
          nodesConnectable={false}
          onEdgesChange={onEdgesChange}
          onNodeClick={(_event, node) => {
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
          onNodesChange={onNodesChange}
          onlyRenderVisibleElements
          panOnScroll
          proOptions={{ hideAttribution: true }}
          selectionOnDrag
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
        {resourceOverlay}
      </section>
    </div>
  );
};
