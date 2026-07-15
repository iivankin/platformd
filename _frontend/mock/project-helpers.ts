import type { BackupPolicy } from "../web/api";
import type { MockState } from "./state";
import { mockNow } from "./state";

export const projectByID = (state: MockState, projectID: string) =>
  state.projects.find((project) => project.id === projectID);

export const touchProject = (
  state: MockState,
  projectID: string,
  field?: "objectStoreCount" | "postgresCount" | "redisCount" | "serviceCount"
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
  resourceId: string
) => {
  state.backupPolicies = [
    ...state.backupPolicies,
    {
      enabled: false,
      resourceId,
      resourceKind,
      retentionCount: 5,
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
