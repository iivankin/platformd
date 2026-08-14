import type { ContainerResourceKind } from "@/api";
import { ContainerFileBrowser } from "@/container-file-browser";
import { ContainerTerminalOverlay } from "@/container-terminal-overlay";
import { Modal } from "@/errors/dialog-frame";

export type RuntimeTool = "console" | "files";

export const RuntimeToolDialogs = ({
  onChange,
  projectID,
  resourceID,
  resourceKind,
  resourceName,
  tool,
}: {
  onChange: (tool?: RuntimeTool) => void;
  projectID: string;
  resourceID: string;
  resourceKind: ContainerResourceKind;
  resourceName: string;
  tool?: RuntimeTool;
}) => (
  <>
    <Modal
      className="max-w-[min(76rem,calc(100vw-2rem))]"
      description={`Interactive shell for ${resourceName}'s current deployment.`}
      onOpenChange={(open) => onChange(open ? "console" : undefined)}
      open={tool === "console"}
      title="Container console"
    >
      <ContainerTerminalOverlay
        className="border-0 ring-0"
        embedded
        projectID={projectID}
        resourceID={resourceID}
        resourceKind={resourceKind}
        resourceName={resourceName}
      />
    </Modal>
    <Modal
      className="max-w-[min(92rem,calc(100vw-2rem))]"
      description="Browse and transfer files in the currently running container."
      onOpenChange={(open) => onChange(open ? "files" : undefined)}
      open={tool === "files"}
      title={`${resourceName} container files`}
    >
      <ContainerFileBrowser
        className="border-0 ring-0"
        projectID={projectID}
        resourceID={resourceID}
        resourceKind={resourceKind}
      />
    </Modal>
  </>
);
