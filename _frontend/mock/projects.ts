import type { Project } from "../web/api";
import { apiSegments, json, mockError, readObject, stringField } from "./http";
import { handleManagedResourcesAPI } from "./managed-resources";
import { handleResourceCreation } from "./project-resources";
import { handleServicesAPI } from "./services";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

const handleProjectCollection = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, ...rest] = segments;
  if (root !== "projects" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET") {
    return json(state.projects);
  }
  if (request.method !== "POST") {
    return undefined;
  }
  const input = await readObject(request);
  const project: Project = {
    createdAt: mockNow(),
    id: nextMockID(state, "project"),
    name: stringField(input, "name", "mock-project"),
    objectStoreCount: 0,
    postgresCount: 0,
    redisCount: 0,
    serviceCount: 0,
    updatedAt: mockNow(),
  };
  state.projects = [...state.projects, project];
  state.canvases[project.id] = {
    connections: [],
    project,
    resources: [],
  };
  return json(project, 201);
};

const handleCanvas = (
  request: Request,
  state: MockState,
  segments: string[]
): Response | undefined => {
  const [root, projectID, resource, ...rest] = segments;
  if (
    request.method !== "GET" ||
    root !== "projects" ||
    !projectID ||
    resource !== "canvas" ||
    rest.length > 0
  ) {
    return undefined;
  }
  return state.canvases[projectID]
    ? json(state.canvases[projectID])
    : mockError("not_found", "Project not found", 404);
};

export const handleProjectsAPI = async (
  request: Request,
  state: MockState,
  pathname: string,
  url: URL
): Promise<Response | undefined> => {
  const segments = apiSegments(pathname);
  return (
    (await handleProjectCollection(request, state, segments)) ??
    handleCanvas(request, state, segments) ??
    (await handleResourceCreation(request, state, segments)) ??
    (await handleServicesAPI(request, state, segments, url)) ??
    handleManagedResourcesAPI(request, state, segments, url)
  );
};
