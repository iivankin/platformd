import type { ResourceLogKind } from "@/api";
import { ResourceLogs } from "@/resource-logs";

export const DeploymentLogs = ({
  deploymentID,
  kind,
  projectID,
  resourceID,
}: {
  deploymentID: string;
  kind: ResourceLogKind;
  projectID: string;
  resourceID: string;
}) => (
  <ResourceLogs
    deploymentID={deploymentID}
    description="Runtime output from this deployment only."
    key={deploymentID}
    kind={kind}
    projectID={projectID}
    resourceID={resourceID}
    title="Deploy logs"
  />
);
