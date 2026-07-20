import {
  createManagedPostgres,
  createManagedRedis,
  createNetworkGateway,
  createObjectStore,
  createService,
} from "@/api";
import type {
  CreateBackupPolicyInput,
  CreateManagedPostgresInput,
  CreateManagedRedisInput,
  NetworkGatewayInput,
  CreateObjectStoreInput,
  CreateServiceInput,
  ProjectCanvas,
} from "@/api";
import {
  parseServiceConfiguration,
  serviceConfigurationDraftFromCreateInput,
} from "@/service-configuration";
import type { ServiceSettingsDraft } from "@/service-settings-model";

export type PendingServiceCreationSettings = ServiceSettingsDraft;

export type PendingBackupPolicy = CreateBackupPolicyInput;

export const emptyPendingBackupPolicy = (): PendingBackupPolicy => ({
  cron: "0 3 * * *",
  enabled: false,
  retentionCount: 7,
  targetId: "",
});

export const emptyPendingServiceCreationSettings = (
  input: CreateServiceInput
): PendingServiceCreationSettings => ({
  configuration: serviceConfigurationDraftFromCreateInput(input),
  domains: [],
  listeners: [],
  volumeMounts: [],
  volumes: [],
});

export type PendingResourceCreation =
  | {
      id: string;
      input: NetworkGatewayInput;
      kind: "network_gateway";
    }
  | {
      id: string;
      input: CreateManagedPostgresInput;
      kind: "postgres";
      backupPolicy: PendingBackupPolicy;
    }
  | {
      id: string;
      input: CreateManagedRedisInput;
      kind: "redis";
      backupPolicy: PendingBackupPolicy;
    }
  | {
      id: string;
      input: CreateObjectStoreInput;
      kind: "storage";
      backupPolicy: PendingBackupPolicy;
    }
  | {
      id: string;
      input: CreateServiceInput;
      kind: "service";
      settings: PendingServiceCreationSettings;
    };

export const newResourceDraftID = () => `draft:${crypto.randomUUID()}`;

const resourceLabels: Record<PendingResourceCreation["kind"], string> = {
  network_gateway: "Network gateway",
  postgres: "PostgreSQL",
  redis: "Redis",
  service: "Service",
  storage: "Object storage",
};

export const pendingResourceLabel = (draft: PendingResourceCreation) =>
  resourceLabels[draft.kind];

export const pendingResourceName = (draft: PendingResourceCreation) =>
  draft.input.name;

export interface PendingResourceChangeDetail {
  detail: string;
  id: string;
  label: string;
}

const createBackupPolicyInput = (policy: PendingBackupPolicy) => {
  if (
    !Number.isInteger(policy.retentionCount) ||
    policy.retentionCount < 1 ||
    policy.retentionCount > 100
  ) {
    throw new Error("Backup retention must be between 1 and 100");
  }
  if (policy.enabled && !(policy.targetId && policy.cron.trim())) {
    throw new Error("Automatic backups require storage and a cron schedule");
  }
  return {
    ...policy,
    cron: policy.enabled ? policy.cron.trim() : "",
  };
};

export const pendingResourceChangeDetails = (
  draft: PendingResourceCreation
): PendingResourceChangeDetail[] => {
  const details: PendingResourceChangeDetail[] = [
    {
      detail: pendingResourceLabel(draft),
      id: `create:${draft.id}`,
      label: "Create resource",
    },
  ];
  if (draft.kind !== "service") {
    if (draft.kind === "network_gateway") {
      return details;
    }
    if (draft.backupPolicy.enabled) {
      details.push({
        detail: `${draft.backupPolicy.cron} · keep ${draft.backupPolicy.retentionCount}`,
        id: `backups:${draft.id}`,
        label: "Configure backups",
      });
    }
    return details;
  }
  for (const domain of draft.settings.domains) {
    details.push({
      detail: `${domain.hostname} → :${domain.targetPort}`,
      id: `domain:${domain.hostname}`,
      label: "Add domain",
    });
  }
  for (const listener of draft.settings.listeners) {
    details.push({
      detail: `${listener.protocol.toUpperCase()} :${listener.publicPort} → :${listener.targetPort}`,
      id: `listener:${listener.protocol}:${listener.publicPort}`,
      label: "Add listener",
    });
  }
  const mounts = new Map(
    draft.settings.volumeMounts.map((mount) => [
      mount.volumeId,
      mount.containerPath,
    ])
  );
  for (const volume of draft.settings.volumes) {
    const mountPath = mounts.get(volume.id);
    details.push({
      detail: mountPath ? `${volume.name} → ${mountPath}` : volume.name,
      id: `volume:${volume.id}`,
      label: "Add volume",
    });
  }
  return details;
};

export const applyPendingResource = (
  projectID: string,
  draft: PendingResourceCreation
) => {
  switch (draft.kind) {
    case "postgres": {
      return createManagedPostgres(projectID, {
        ...draft.input,
        backupPolicy: createBackupPolicyInput(draft.backupPolicy),
      });
    }
    case "network_gateway": {
      return createNetworkGateway(projectID, draft.input);
    }
    case "redis": {
      return createManagedRedis(projectID, {
        ...draft.input,
        backupPolicy: createBackupPolicyInput(draft.backupPolicy),
      });
    }
    case "service": {
      const configuration = parseServiceConfiguration(
        draft.settings.configuration,
        draft.settings.domains.length
      );
      const mounts = new Map(
        draft.settings.volumeMounts.map((mount) => [
          mount.volumeId,
          mount.containerPath,
        ])
      );
      return createService(projectID, {
        ...draft.input,
        domains: draft.settings.domains,
        healthCheck: configuration.healthCheck,
        listeners: draft.settings.listeners,
        name: draft.input.name.trim(),
        registryCredential: configuration.registryCredential,
        source: configuration.source,
        volumes: draft.settings.volumes.map((volume) => ({
          containerPath: mounts.get(volume.id),
          name: volume.name,
          ownerGid: volume.ownerGid,
          ownerUid: volume.ownerUid,
        })),
      });
    }
    case "storage": {
      return createObjectStore(projectID, {
        ...draft.input,
        backupPolicy: createBackupPolicyInput(draft.backupPolicy),
      });
    }
    default: {
      throw new Error("Unsupported resource draft");
    }
  }
};

export const pendingCanvasResource = (
  draft: PendingResourceCreation,
  projectName: string
): ProjectCanvas["resources"][number] => {
  const common = {
    enabled: true,
    id: draft.id,
    internalHostname: `${draft.input.name}.${projectName}.internal`,
    name: draft.input.name,
    status: "pending" as const,
    statusMessage: "Local draft · deploy to create",
    volumes: [],
  };
  switch (draft.kind) {
    case "postgres": {
      return {
        ...common,
        imageReference: `postgres:${draft.input.imageTag}`,
        kind: "postgres",
      };
    }
    case "network_gateway": {
      return {
        ...common,
        gatewayListenPort: draft.input.listenPort,
        gatewayMode: draft.input.mode,
        gatewayProtocol: draft.input.protocol,
        gatewayRemoteHost: draft.input.remoteHost || undefined,
        gatewayRemotePort: draft.input.remotePort || undefined,
        gatewaySourceAddress: draft.input.sourceAddress,
        gatewayTargetPort: draft.input.targetPort || undefined,
        gatewayTargetServiceId: draft.input.targetServiceId || undefined,
        gatewayTransport: draft.input.transport,
        internalHostname:
          draft.input.mode === "import"
            ? common.internalHostname
            : `${draft.input.sourceAddress}:${draft.input.listenPort}`,
        kind: "network_gateway",
      };
    }
    case "redis": {
      return {
        ...common,
        imageReference: `redis:${draft.input.imageTag}`,
        kind: "redis",
      };
    }
    case "service": {
      const mountPaths = new Map(
        draft.settings.volumeMounts.map((mount) => [
          mount.volumeId,
          mount.containerPath,
        ])
      );
      return {
        ...common,
        kind: "service",
        source: draft.settings.configuration.source,
        volumes: draft.settings.volumes.map((volume) => ({
          containerPath: mountPaths.get(volume.id),
          id: volume.id,
          name: volume.name,
        })),
      };
    }
    case "storage": {
      return {
        ...common,
        bucketName: draft.input.bucketName,
        kind: "object_store",
      };
    }
    default: {
      throw new Error("Unsupported resource draft");
    }
  }
};
