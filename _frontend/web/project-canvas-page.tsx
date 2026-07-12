import {
  Background,
  BackgroundVariant,
  Controls,
  ReactFlow,
  useEdgesState,
  useNodesState,
} from "@xyflow/react";
import { Plus, Waypoints } from "lucide-react";
import { useEffect, useState } from "react";
import { useParams } from "react-router";

import { fetchImageCredentials, fetchProjectCanvas } from "@/api";
import type { ImageCredential, ProjectCanvas } from "@/api";
import { Button } from "@/components/ui/button";
import { mergeResourceNodeData, projectFlowElements } from "@/project-flow";
import type { ResourceFlowEdge, ResourceFlowNode } from "@/project-flow";
import { ResourceDetailPanel } from "@/resource-detail-panel";
import { ResourceNode } from "@/resource-node";
import { ServiceCreatePanel } from "@/service-create-panel";

const nodeTypes = { resource: ResourceNode };
const emptyNodes: ResourceFlowNode[] = [];
const emptyEdges: ResourceFlowEdge[] = [];
const statusRefreshMilliseconds = 5000;

export const ProjectCanvasPage = () => {
  const { projectID = "" } = useParams();
  const [canvas, setCanvas] = useState<ProjectCanvas | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [credentials, setCredentials] = useState<ImageCredential[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [selectedNodeID, setSelectedNodeID] = useState<string | null>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [nodes, setNodes, onNodesChange] =
    useNodesState<ResourceFlowNode>(emptyNodes);
  const [edges, setEdges, onEdgesChange] =
    useEdgesState<ResourceFlowEdge>(emptyEdges);
  const selectedNode = nodes.find((node) => node.id === selectedNodeID);

  useEffect(() => {
    const controller = new AbortController();
    let refreshTimer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const [loaded, loadedCredentials] = await Promise.all([
          fetchProjectCanvas(projectID, controller.signal),
          fetchImageCredentials(projectID, controller.signal).catch(() => []),
        ]);
        const flow = projectFlowElements(loaded);
        setCanvas(loaded);
        setNodes((current) => mergeResourceNodeData(current, flow.nodes));
        setEdges(flow.edges);
        setCredentials(loadedCredentials);
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
        <Button onClick={() => setCreateOpen(true)} size="sm">
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
        {createOpen ? (
          <ServiceCreatePanel
            credentials={credentials}
            onClose={() => setCreateOpen(false)}
            onCreated={() => {
              setCreateOpen(false);
              setRefreshVersion((value) => value + 1);
            }}
            projectID={projectID}
          />
        ) : null}
        {!createOpen && selectedNode ? (
          <ResourceDetailPanel
            data={selectedNode.data}
            onClose={() => setSelectedNodeID(null)}
          />
        ) : null}
        {canvas && canvas.resources.length === 0 ? (
          <div className="pointer-events-none absolute inset-0 z-10 grid place-items-center text-center">
            <div className="max-w-sm">
              <Waypoints className="mx-auto size-7 text-muted-foreground" />
              <h2 className="mt-5 text-sm font-medium">Empty project canvas</h2>
              <p className="mt-2 text-xs leading-5 text-muted-foreground">
                Add a service, PostgreSQL, Redis, or object store. Connections
                appear automatically from service environment values.
              </p>
            </div>
          </div>
        ) : null}
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
          onNodeClick={(_event, node) => setSelectedNodeID(node.id)}
          onNodesChange={onNodesChange}
          onPaneClick={() => setSelectedNodeID(null)}
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
      </section>
    </div>
  );
};
