import type { BackupPolicy, RecoveryResourceKind } from "../web/api";
import {
  booleanField,
  json,
  mockError,
  noContent,
  numberField,
  readObject,
  stringField,
} from "./http";
import type { MockState } from "./state";
import { mockNow, mockResourceKey, nextMockID } from "./state";

const backupKinds = new Set<RecoveryResourceKind>([
  "object_store",
  "postgres",
  "redis",
  "registry",
]);

const backupKind = (value: string): RecoveryResourceKind | undefined =>
  backupKinds.has(value as RecoveryResourceKind)
    ? (value as RecoveryResourceKind)
    : undefined;

const handleBackupTarget = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, resource, ...rest] = segments;
  if (root !== "backups" || resource !== "target" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET") {
    return json(state.backupTarget);
  }
  if (request.method === "DELETE") {
    state.backupTarget = { configured: false };
    return noContent();
  }
  if (request.method !== "PUT") {
    return undefined;
  }
  const input = await readObject(request);
  state.backupTarget = {
    accessKeyId: stringField(input, "accessKeyId"),
    bucket: stringField(input, "bucket"),
    configured: true,
    createdAt: state.backupTarget.createdAt ?? mockNow(),
    endpoint: stringField(input, "endpoint"),
    prefix: stringField(input, "prefix"),
    region: stringField(input, "region"),
    updatedAt: mockNow(),
  };
  return json(state.backupTarget);
};

const handlePolicy = async (
  request: Request,
  state: MockState,
  kind: RecoveryResourceKind,
  resourceID: string
): Promise<Response | undefined> => {
  if (request.method === "GET") {
    const policy = state.backupPolicies.find(
      (candidate) =>
        candidate.resourceKind === kind && candidate.resourceId === resourceID
    );
    return policy
      ? json(policy)
      : mockError("not_found", "Backup policy not found", 404);
  }
  if (request.method !== "PUT") {
    return undefined;
  }
  const input = await readObject(request);
  const policy: BackupPolicy = {
    cron: stringField(input, "cron"),
    enabled: booleanField(input, "enabled"),
    nextRunAt: mockNow() + 3_600_000,
    resourceId: resourceID,
    resourceKind: kind,
    retentionCount: numberField(input, "retentionCount", 7),
  };
  state.backupPolicies = [
    ...state.backupPolicies.filter(
      (candidate) =>
        !(
          candidate.resourceKind === kind && candidate.resourceId === resourceID
        )
    ),
    policy,
  ];
  return json(policy);
};

const handleBackupRecords = (
  request: Request,
  state: MockState,
  kind: RecoveryResourceKind,
  resourceID: string,
  action: string
): Response | undefined => {
  const key = mockResourceKey(kind, resourceID);
  if (request.method === "GET" && action === "history") {
    return json({ backups: state.backupHistory[key] ?? [] });
  }
  if (request.method === "GET" && action === "generations") {
    return json({ generations: state.backupGenerations[key] ?? [] });
  }
  if (request.method !== "POST" || action !== "run") {
    return undefined;
  }
  const record = {
    id: nextMockID(state, "backup"),
    resourceId: resourceID,
    resourceKind: kind,
    startedAt: mockNow(),
    status: "running" as const,
  };
  state.backupHistory[key] = [record, ...(state.backupHistory[key] ?? [])];
  return json(record);
};

const handleRestore = (
  request: Request,
  state: MockState,
  resourceID: string,
  action: string
): Response | undefined => {
  if (request.method !== "POST" || action !== "restore") {
    return undefined;
  }
  const operation = {
    id: nextMockID(state, "operation"),
    kind: "backup_restore",
    progress: "complete",
    startedAt: mockNow(),
    status: "succeeded" as const,
    targetId: resourceID,
  };
  state.operations[operation.id] = operation;
  return json(operation);
};

const handleBackupResource = (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> | Response | undefined => {
  const [root, collection, kindValue, resourceID, action, ...rest] = segments;
  if (
    root !== "backups" ||
    collection !== "resources" ||
    !kindValue ||
    !resourceID ||
    !action ||
    rest.length > 0
  ) {
    return undefined;
  }
  const kind = backupKind(kindValue);
  if (!kind) {
    return mockError("invalid_kind", "Unknown backup resource kind");
  }
  return action === "policy"
    ? handlePolicy(request, state, kind, resourceID)
    : (handleBackupRecords(request, state, kind, resourceID, action) ??
        handleRestore(request, state, resourceID, action));
};

export const handleBackupsAPI = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const targetResponse = await handleBackupTarget(request, state, segments);
  if (targetResponse) {
    return targetResponse;
  }
  const [root, resource, ...rest] = segments;
  if (
    request.method === "GET" &&
    root === "backups" &&
    resource === "resources" &&
    rest.length === 0
  ) {
    return json({ policies: state.backupPolicies });
  }
  return handleBackupResource(request, state, segments);
};
