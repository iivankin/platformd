import { handleContainerResourcesAPI } from "./container-resources";
import { handleCoreAPI } from "./core";
import { mockError } from "./http";
import { handleProjectsAPI } from "./projects";
import { handleRegistryAPI } from "./registry";
import type { MockState } from "./state";

export const handleMockAPI = async (
  request: Request,
  state: MockState
): Promise<Response> => {
  const url = new URL(request.url);
  if (state.scenario === "error") {
    return mockError(
      "mock_unavailable",
      "The error scenario makes every mocked API request fail.",
      503
    );
  }

  const containerResponse = await handleContainerResourcesAPI(
    request,
    state,
    url.pathname,
    url
  );
  if (containerResponse) {
    return containerResponse;
  }

  const projectsResponse = await handleProjectsAPI(
    request,
    state,
    url.pathname,
    url
  );
  if (projectsResponse) {
    return projectsResponse;
  }
  const registryResponse = await handleRegistryAPI(
    request,
    state,
    url.pathname
  );
  if (registryResponse) {
    return registryResponse;
  }
  const coreResponse = await handleCoreAPI(request, state, url.pathname, url);
  if (coreResponse) {
    return coreResponse;
  }
  return mockError(
    "mock_not_implemented",
    `No mock handler for ${request.method} ${url.pathname}`,
    501
  );
};
