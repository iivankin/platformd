import type { ErrorTracker } from "../web/api";
import { handleErrorTrackerMock } from "./error-tracker-router";
import { createErrorTrackerMockState } from "./error-tracker-state";
import {
  booleanField,
  json,
  mockError,
  numberField,
  readObject,
  stringField,
} from "./http";
import { addBackupPolicy, touchProject } from "./project-helpers";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

const refreshConsoleOrigin = (state: MockState, resource: ErrorTracker) => {
  const consoleState = state.errorTrackerConsoles[resource.id];
  if (!consoleState) {
    return;
  }
  const publicUrl = resource.publicHostname
    ? `https://${resource.publicHostname}`
    : resource.internalUrl;
  consoleState.tracker.publicUrl = publicUrl;
  for (const app of consoleState.apps) {
    const dsn = new URL(publicUrl);
    dsn.username = app.publicKey;
    dsn.pathname = `/${app.projectId}`;
    app.dsn = dsn.toString().replace(/\/$/u, "");
  }
};

const createTracker = async (
  request: Request,
  state: MockState,
  projectID: string
) => {
  const canvas = state.canvases[projectID];
  if (!canvas) {
    return mockError("project_not_found", "Project not found", 404);
  }
  const input = await readObject(request);
  const name = stringField(input, "name", "errors");
  const id = nextMockID(state, "error-tracker");
  const createdAt = mockNow();
  const internalHostname = `${name}.${canvas.project.name}.internal`;
  const backup =
    input.backupPolicy && typeof input.backupPolicy === "object"
      ? (input.backupPolicy as Record<string, unknown>)
      : {};
  const resource: ErrorTracker = {
    backupCron: stringField(backup, "cron") || undefined,
    backupEnabled: booleanField(backup, "enabled", false),
    backupRetentionCount: numberField(backup, "retentionCount", 7),
    createdAt,
    id,
    internalHostname,
    internalUrl: `http://${internalHostname}:9001`,
    name,
    projectId: projectID,
    publicHostname: stringField(input, "publicHostname") || undefined,
    status: "running",
    updatedAt: createdAt,
    volumeId: nextMockID(state, "volume"),
  };
  state.errorTrackers[id] = resource;
  state.errorTrackerConsoles[id] = createErrorTrackerMockState();
  state.errorTrackerConsoles[id].tracker.name = name;
  state.errorTrackerConsoles[id].tracker.slug = name;
  refreshConsoleOrigin(state, resource);
  canvas.resources.push({
    enabled: true,
    id,
    internalHostname,
    kind: "error_tracker",
    name,
    status: "running",
    volumes: [],
  });
  addBackupPolicy(state, "error_tracker", id, {
    cron: resource.backupCron,
    enabled: resource.backupEnabled,
    retentionCount: resource.backupRetentionCount,
    targetId: stringField(backup, "targetId") || undefined,
  });
  touchProject(state, projectID, "errorTrackerCount");
  return json(resource, 201);
};

const updatePublicAccess = async (
  request: Request,
  state: MockState,
  resource: ErrorTracker
) => {
  const input = await readObject(request);
  if (numberField(input, "expectedUpdatedAt", -1) !== resource.updatedAt) {
    return mockError("error_tracker_changed", "Error tracker changed", 409);
  }
  const updated: ErrorTracker = {
    ...resource,
    publicHostname: stringField(input, "publicHostname") || undefined,
    updatedAt: mockNow(),
  };
  state.errorTrackers[resource.id] = updated;
  refreshConsoleOrigin(state, updated);
  return json(updated);
};

export const handlePlatformErrorTrackers = (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response> | Response | undefined => {
  const [root, projectID, collection, trackerID, action, ...tail] = segments;
  if (root !== "projects" || !projectID || collection !== "error-trackers") {
    return undefined;
  }
  if (request.method === "POST" && !trackerID) {
    return createTracker(request, state, projectID);
  }
  if (!trackerID) {
    return undefined;
  }
  const resource = state.errorTrackers[trackerID];
  if (!resource || resource.projectId !== projectID) {
    return mockError("error_tracker_not_found", "Error tracker not found", 404);
  }
  if (action === "console") {
    const consoleState = state.errorTrackerConsoles[trackerID];
    if (!consoleState) {
      return mockError("error_tracker_unavailable", "Tracker unavailable", 503);
    }
    const url = new URL(request.url);
    url.pathname = `/${tail.join("/")}`;
    return handleErrorTrackerMock(new Request(url, request), consoleState);
  }
  if (
    request.method === "PUT" &&
    action === "public-access" &&
    tail.length === 0
  ) {
    return updatePublicAccess(request, state, resource);
  }
  if (request.method === "GET" && !action) {
    return json(resource);
  }
  return undefined;
};
