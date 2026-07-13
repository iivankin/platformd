import type { ImageCredential } from "@/api";
import { ObjectStoreCreatePanel } from "@/object-store-create-panel";
import { PostgresCreatePanel } from "@/postgres-create-panel";
import { RedisCreatePanel } from "@/redis-create-panel";
import { ResourceCreatePanel } from "@/resource-create-panel";
import { ServiceCreatePanel } from "@/service-create-panel";

export type CreateKind =
  | "picker"
  | "postgres"
  | "redis"
  | "service"
  | "storage"
  | null;

interface ProjectCreateOverlaysProperties {
  credentials: ImageCredential[];
  kind: CreateKind;
  onClose: () => void;
  onCreated: () => void;
  onSelect: (kind: "postgres" | "redis" | "service" | "storage") => void;
  projectID: string;
}

export const ProjectCreateOverlays = ({
  credentials,
  kind,
  onClose,
  onCreated,
  onSelect,
  projectID,
}: ProjectCreateOverlaysProperties) => (
  <>
    {kind === "picker" ? (
      <ResourceCreatePanel onClose={onClose} onSelect={onSelect} />
    ) : null}
    {kind === "service" ? (
      <ServiceCreatePanel
        credentials={credentials}
        onClose={onClose}
        onCreated={onCreated}
        projectID={projectID}
      />
    ) : null}
    {kind === "redis" ? (
      <RedisCreatePanel
        onClose={onClose}
        onCreated={onCreated}
        projectID={projectID}
      />
    ) : null}
    {kind === "postgres" ? (
      <PostgresCreatePanel
        onClose={onClose}
        onCreated={onCreated}
        projectID={projectID}
      />
    ) : null}
    {kind === "storage" ? (
      <ObjectStoreCreatePanel
        onClose={onClose}
        onCreated={onCreated}
        projectID={projectID}
      />
    ) : null}
  </>
);
