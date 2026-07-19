import { ObjectStoreCreatePanel } from "@/object-store-create-panel";
import { newResourceDraftID } from "@/pending-resource-creation";
import type { PendingResourceCreation } from "@/pending-resource-creation";
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
  embeddedRegistryHost: string;
  draft?: PendingResourceCreation;
  kind: CreateKind;
  onClose: () => void;
  onDrafted: (draft: PendingResourceCreation) => void;
  onSelect: (kind: "postgres" | "redis" | "service" | "storage") => void;
}

export const ProjectCreateOverlays = ({
  draft,
  embeddedRegistryHost,
  kind,
  onClose,
  onDrafted,
  onSelect,
}: ProjectCreateOverlaysProperties) => (
  <>
    {kind === "picker" ? (
      <ResourceCreatePanel onClose={onClose} onSelect={onSelect} />
    ) : null}
    {kind === "service" ? (
      <ServiceCreatePanel
        embeddedRegistryHost={embeddedRegistryHost}
        onClose={onClose}
        initialDraft={draft?.kind === "service" ? draft.input : undefined}
        onDrafted={(input) =>
          onDrafted({
            id: draft?.id ?? newResourceDraftID(),
            input,
            kind: "service",
          })
        }
      />
    ) : null}
    {kind === "redis" ? (
      <RedisCreatePanel
        onClose={onClose}
        initialDraft={draft?.kind === "redis" ? draft.input : undefined}
        onDrafted={(input) =>
          onDrafted({
            id: draft?.id ?? newResourceDraftID(),
            input,
            kind: "redis",
          })
        }
      />
    ) : null}
    {kind === "postgres" ? (
      <PostgresCreatePanel
        onClose={onClose}
        initialDraft={draft?.kind === "postgres" ? draft.input : undefined}
        onDrafted={(input) =>
          onDrafted({
            id: draft?.id ?? newResourceDraftID(),
            input,
            kind: "postgres",
          })
        }
      />
    ) : null}
    {kind === "storage" ? (
      <ObjectStoreCreatePanel
        onClose={onClose}
        initialDraft={draft?.kind === "storage" ? draft.input : undefined}
        onDrafted={(input) =>
          onDrafted({
            id: draft?.id ?? newResourceDraftID(),
            input,
            kind: "storage",
          })
        }
      />
    ) : null}
  </>
);
