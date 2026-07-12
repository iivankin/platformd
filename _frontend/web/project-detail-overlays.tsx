import { PostgresDetailPanel } from "@/postgres-detail-panel";
import type { ResourceFlowNode } from "@/project-flow";
import { RedisDetailPanel } from "@/redis-detail-panel";
import { ResourceDetailPanel } from "@/resource-detail-panel";
import { ServiceDetailPanel } from "@/service-detail-panel";

interface ProjectDetailOverlaysProperties {
  createOpen: boolean;
  onChanged: () => void;
  onClose: () => void;
  projectID: string;
  selectedNode?: ResourceFlowNode;
}

export const ProjectDetailOverlays = ({
  createOpen,
  onChanged,
  onClose,
  projectID,
  selectedNode,
}: ProjectDetailOverlaysProperties) => {
  if (createOpen || !selectedNode) {
    return null;
  }
  switch (selectedNode.data.kind) {
    case "service": {
      return (
        <ServiceDetailPanel
          data={selectedNode.data}
          onChanged={onChanged}
          onClose={onClose}
          projectID={projectID}
          serviceID={selectedNode.id}
        />
      );
    }
    case "redis": {
      return (
        <RedisDetailPanel
          data={selectedNode.data}
          onChanged={onChanged}
          onClose={onClose}
          projectID={projectID}
          redisID={selectedNode.id}
        />
      );
    }
    case "postgres": {
      return (
        <PostgresDetailPanel
          data={selectedNode.data}
          onClose={onClose}
          postgresID={selectedNode.id}
          projectID={projectID}
        />
      );
    }
    default: {
      return <ResourceDetailPanel data={selectedNode.data} onClose={onClose} />;
    }
  }
};
