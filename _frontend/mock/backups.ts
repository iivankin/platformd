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
import { mockBackupTargetKey, mockNow, nextMockID } from "./state";

const backupKinds = new Set<RecoveryResourceKind>([
  "object_store",
  "postgres",
  "redis",
  "registry",
  "volume",
]);

const backupKind = (value: string): RecoveryResourceKind | undefined =>
  backupKinds.has(value as RecoveryResourceKind)
    ? (value as RecoveryResourceKind)
    : undefined;

const targetFromInput = (
  input: Record<string, unknown>,
  id: string,
  createdAt: number
) => ({
  accessKeyId: stringField(input, "accessKeyId"),
  bucket: stringField(input, "bucket"),
  createdAt,
  endpoint: stringField(input, "endpoint"),
  id,
  name: stringField(input, "name"),
  prefix: stringField(input, "prefix"),
  region: stringField(input, "region"),
  updatedAt: mockNow(),
});

const handleBackupTargets = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, collection, targetID, ...rest] = segments;
  if (root !== "backups") {
    return undefined;
  }
  if (
    collection === "control-target" &&
    !targetID &&
    rest.length === 0 &&
    request.method === "PUT"
  ) {
    const input = await readObject(request);
    state.backupControlTargetId = stringField(input, "targetId");
    return json({ targetId: state.backupControlTargetId });
  }
  if (collection !== "targets" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !targetID) {
    return json({
      controlTargetId: state.backupControlTargetId,
      targets: state.backupTargets,
    });
  }
  if (request.method === "POST" && !targetID) {
    const input = await readObject(request);
    const target = targetFromInput(
      input,
      nextMockID(state, "backup-target"),
      mockNow()
    );
    state.backupTargets.push(target);
    return json(target);
  }
  if (!targetID) {
    return undefined;
  }
  const index = state.backupTargets.findIndex(
    (target) => target.id === targetID
  );
  if (index === -1) {
    return mockError(
      "backup_target_not_found",
      "Backup storage not found",
      404
    );
  }
  if (request.method === "DELETE") {
    if (
      state.backupControlTargetId === targetID ||
      state.backupPolicies.some((policy) => policy.targetId === targetID)
    ) {
      return mockError(
        "backup_target_in_use",
        "Backup storage is still selected",
        409
      );
    }
    state.backupTargets.splice(index, 1);
    return noContent();
  }
  if (request.method === "PUT") {
    const input = await readObject(request);
    const existingTarget = state.backupTargets[index];
    if (existingTarget === undefined) {
      return mockError(
        "backup_target_not_found",
        "Backup storage not found",
        404
      );
    }
    const target = targetFromInput(input, targetID, existingTarget.createdAt);
    state.backupTargets[index] = target;
    return json(target);
  }
  return undefined;
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
      : json({
          enabled: false,
          resourceId: resourceID,
          resourceKind: kind,
          retentionCount: 7,
        });
  }
  if (request.method !== "PUT") {
    return undefined;
  }
  const input = await readObject(request);
  const cron = stringField(input, "cron");
  const enabled = booleanField(input, "enabled");
  const targetID = stringField(input, "targetId");
  if (
    targetID &&
    !state.backupTargets.some((target) => target.id === targetID)
  ) {
    return mockError(
      "backup_target_not_found",
      "Backup storage not found",
      404
    );
  }
  if (enabled && (!cron || !targetID)) {
    return mockError(
      "invalid_backup_resource",
      "Automatic backups require a storage and schedule"
    );
  }
  const policy: BackupPolicy = {
    cron,
    enabled,
    nextRunAt: enabled && cron ? mockNow() + 3_600_000 : undefined,
    resourceId: resourceID,
    resourceKind: kind,
    retentionCount: numberField(input, "retentionCount", 7),
    targetId: targetID,
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

const handleBackupRecords = async (
  request: Request,
  state: MockState,
  kind: RecoveryResourceKind,
  resourceID: string,
  action: string
): Promise<Response | undefined> => {
  const requestedTargetID =
    new URL(request.url).searchParams.get("targetId") ?? "";
  const key = mockBackupTargetKey(kind, resourceID, requestedTargetID);
  if (request.method === "GET" && action === "history") {
    return json({ backups: state.backupHistory[key] ?? [] });
  }
  if (request.method === "GET" && action === "generations") {
    return json({ generations: state.backupGenerations[key] ?? [] });
  }
  if (request.method !== "POST" || action !== "run") {
    return undefined;
  }
  const input = await readObject(request);
  const targetID = stringField(input, "targetId");
  if (!state.backupTargets.some((target) => target.id === targetID)) {
    return mockError(
      "backup_target_not_found",
      "Backup storage not found",
      404
    );
  }
  const runKey = mockBackupTargetKey(kind, resourceID, targetID);
  const record = {
    id: nextMockID(state, "backup"),
    resourceId: resourceID,
    resourceKind: kind,
    startedAt: mockNow(),
    status: "running" as const,
    targetId: targetID,
  };
  state.backupHistory[runKey] = [
    record,
    ...(state.backupHistory[runKey] ?? []),
  ];
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

const handleBackupResource = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
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
  if (action === "policy") {
    return handlePolicy(request, state, kind, resourceID);
  }
  return (
    (await handleBackupRecords(request, state, kind, resourceID, action)) ??
    handleRestore(request, state, resourceID, action)
  );
};

export const handleBackupsAPI = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const targetResponse = await handleBackupTargets(request, state, segments);
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
