import type { BackupPolicy } from "../web/api";
import type { MockState } from "./state";
import { mockNow } from "./state";

export const projectByID = (state: MockState, projectID: string) =>
  state.projects.find((project) => project.id === projectID);

export const touchProject = (
  state: MockState,
  projectID: string,
  field?:
    | "networkGatewayCount"
    | "objectStoreCount"
    | "postgresCount"
    | "redisCount"
    | "serviceCount"
) => {
  const project = projectByID(state, projectID);
  if (!project) {
    return;
  }
  project.updatedAt = mockNow();
  if (field) {
    project[field] += 1;
  }
};

export const addBackupPolicy = (
  state: MockState,
  resourceKind: BackupPolicy["resourceKind"],
  resourceId: string,
  policy: Partial<BackupPolicy> = {}
) => {
  state.backupPolicies = [
    ...state.backupPolicies,
    {
      cron: policy.cron,
      enabled: policy.enabled ?? false,
      resourceId,
      resourceKind,
      retentionCount: policy.retentionCount ?? 7,
      targetId: policy.targetId,
    },
  ];
};

export const stringRecord = (value: unknown): Record<string, string> => {
  if (!(value && typeof value === "object" && !Array.isArray(value))) {
    return {};
  }
  return Object.fromEntries(
    Object.entries(value).filter(
      (entry): entry is [string, string] => typeof entry[1] === "string"
    )
  );
};
