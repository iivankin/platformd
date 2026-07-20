import type { ProjectCanvas } from "@/api";
import { NetworkGatewayCreatePanel } from "@/network-gateway-create-panel";
import { ObjectStoreCreatePanel } from "@/object-store-create-panel";
import {
  emptyPendingBackupPolicy,
  emptyPendingServiceCreationSettings,
  newResourceDraftID,
} from "@/pending-resource-creation";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import { PostgresCreatePanel } from "@/postgres-create-panel";
import { RedisCreatePanel } from "@/redis-create-panel";
import { ResourceCreatePanel } from "@/resource-create-panel";
import {
  createObjectStoreDraftCredentials,
  createPostgresDraftCredentials,
  createRedisDraftCredentials,
} from "@/resource-draft-credentials";
import { ServiceCreatePanel } from "@/service-create-panel";

export type CreateKind =
  | "picker"
  | "network_gateway"
  | "postgres"
  | "redis"
  | "service"
  | "storage"
  | null;

interface ProjectCreateOverlaysProperties {
  embeddedRegistryHost: string;
  kind: CreateKind;
  onClose: () => void;
  onDrafted: (draft: PendingResourceCreation) => void;
  onSelect: (
    kind: "network_gateway" | "postgres" | "redis" | "service" | "storage"
  ) => void;
  projectID: string;
  resources: ProjectCanvas["resources"];
}

export const ProjectCreateOverlays = ({
  embeddedRegistryHost,
  kind,
  onClose,
  onDrafted,
  onSelect,
  projectID,
  resources,
}: ProjectCreateOverlaysProperties) => (
  <>
    {kind === "picker" ? (
      <ResourceCreatePanel onClose={onClose} onSelect={onSelect} />
    ) : null}
    {kind === "service" ? (
      <ServiceCreatePanel
        embeddedRegistryHost={embeddedRegistryHost}
        onClose={onClose}
        onDrafted={(input) => {
          onDrafted({
            id: newResourceDraftID(),
            input,
            kind: "service",
            settings: emptyPendingServiceCreationSettings(input),
          });
        }}
      />
    ) : null}
    {kind === "network_gateway" ? (
      <NetworkGatewayCreatePanel
        onClose={onClose}
        onDrafted={(input) =>
          onDrafted({
            id: newResourceDraftID(),
            input,
            kind: "network_gateway",
          })
        }
        projectID={projectID}
        resources={resources}
      />
    ) : null}
    {kind === "redis" ? (
      <RedisCreatePanel
        onClose={onClose}
        onDrafted={(input) =>
          onDrafted({
            backupPolicy: emptyPendingBackupPolicy(),
            id: newResourceDraftID(),
            input: { ...input, credentials: createRedisDraftCredentials() },
            kind: "redis",
          })
        }
      />
    ) : null}
    {kind === "postgres" ? (
      <PostgresCreatePanel
        onClose={onClose}
        onDrafted={(input) =>
          onDrafted({
            backupPolicy: emptyPendingBackupPolicy(),
            id: newResourceDraftID(),
            input: { ...input, credentials: createPostgresDraftCredentials() },
            kind: "postgres",
          })
        }
      />
    ) : null}
    {kind === "storage" ? (
      <ObjectStoreCreatePanel
        onClose={onClose}
        onDrafted={(input) =>
          onDrafted({
            backupPolicy: emptyPendingBackupPolicy(),
            id: newResourceDraftID(),
            input: {
              ...input,
              credentials: createObjectStoreDraftCredentials(),
            },
            kind: "storage",
          })
        }
      />
    ) : null}
  </>
);
