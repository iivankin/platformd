import type { ContainerResourceKind } from "@/api";
import { ContainerFileBrowser } from "@/container-file-browser";
import { ContainerTerminalOverlay } from "@/container-terminal-overlay";

interface ResourceConsoleProperties {
  projectID: string;
  resourceID: string;
  resourceKind: ContainerResourceKind;
  resourceName: string;
}

export const ResourceConsole = ({
  projectID,
  resourceID,
  resourceKind,
  resourceName,
}: ResourceConsoleProperties) => (
  <div>
    <ContainerTerminalOverlay
      embedded
      projectID={projectID}
      resourceID={resourceID}
      resourceKind={resourceKind}
      resourceName={resourceName}
    />
    <ContainerFileBrowser
      projectID={projectID}
      resourceID={resourceID}
      resourceKind={resourceKind}
    />
  </div>
);
