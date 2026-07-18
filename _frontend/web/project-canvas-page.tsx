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

import {
  fetchImageCredentials,
  fetchProjectCanvas,
  fetchRegistrySettings,
} from "@/api";
import type { ImageCredential, ProjectCanvas } from "@/api";
import { Button } from "@/components/ui/button";
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
import { ResourceNode } from "@/resource-node";
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
  const { deploymentID = "", projectID = "", resourceID = "" } = useParams();
  const [canvas, setCanvas] = useState<ProjectCanvas | null>(null);
  const [canvasError, setCanvasError] = useState<string | null>(null);
  const [metadataError, setMetadataError] = useState<string | null>(null);
  const [credentials, setCredentials] = useState<ImageCredential[]>([]);
  const [embeddedRegistryHost, setEmbeddedRegistryHost] = useState("");
  const [createKind, setCreateKind] = useState<CreateKind>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [applyingChanges, setApplyingChanges] = useState(false);
  const [applyError, setApplyError] = useState<string>();
  const { serviceChanges, setServiceChange } = useProjectChanges(projectID);
  const serviceChangesRef = useRef(serviceChanges);
  const [nodes, setNodes, onNodesChange] =
    useNodesState<ResourceFlowNode>(emptyNodes);
  const [edges, setEdges, onEdgesChange] =
    useEdgesState<ResourceFlowEdge>(emptyEdges);
  const isCanvasEmpty = canvas?.resources.length === 0;
  const error = canvasError ?? metadataError;
  const pendingServices = useMemo(
    () =>
      Object.values(serviceChanges).toSorted((left, right) =>
        left.serviceName.localeCompare(right.serviceName)
      ),
    [serviceChanges]
  );
  let resourceOverlay = null;
  if (deploymentID) {
    resourceOverlay = <ProjectDeploymentPage canvas={canvas} />;
  } else if (resourceID) {
    resourceOverlay = <ProjectResourcePage />;
  }

  useEffect(() => {
    const controller = new AbortController();
    let refreshTimer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const loaded = await fetchProjectCanvas(projectID, controller.signal);
        const flow = projectFlowElements(
          loaded,
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
        const [loadedCredentials, registrySettings] = await Promise.all([
          fetchImageCredentials(projectID, controller.signal).catch(() => []),
          fetchRegistrySettings(controller.signal),
        ]);
        setCredentials(loadedCredentials);
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
    if (canvas) {
      const flow = projectFlowElements(
        canvas,
        resourceOverlays(serviceChanges)
      );
      setNodes((current) => mergeResourceNodeData(current, flow.nodes));
    }
  }, [canvas, serviceChanges, setNodes]);

  const applyChanges = async () => {
    if (applyingChanges || pendingServices.length === 0) {
      return;
    }
    setApplyingChanges(true);
    setApplyError(undefined);
    const results = await Promise.allSettled(
      pendingServices.map((change) =>
        applyServiceSettings(
          projectID,
          change,
          credentials,
          embeddedRegistryHost
        )
      )
    );
    let firstError: string | undefined;
    let applied = false;
    for (const [index, result] of results.entries()) {
      const change = pendingServices[index];
      if (!change) {
        continue;
      }
      if (result.status === "fulfilled") {
        applied = true;
        setServiceChange(change.serviceID);
        continue;
      }
      if (!firstError) {
        const message =
          result.reason instanceof Error
            ? result.reason.message
            : "Unable to apply service settings";
        firstError = `${change.serviceName}: ${message}`;
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
            setApplyError(undefined);
          }}
        />
        <ProjectCreateOverlays
          credentials={credentials}
          embeddedRegistryHost={embeddedRegistryHost}
          kind={createKind}
          onClose={() => setCreateKind(null)}
          onCreated={() => {
            setCreateKind(null);
            setRefreshVersion((value) => value + 1);
          }}
          onSelect={setCreateKind}
          projectID={projectID}
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
          onNodeClick={(_event, node) =>
            void navigate(resourcePath(projectID, node.id, node.data.kind))
          }
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
