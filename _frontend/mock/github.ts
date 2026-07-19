import {
  apiSegments,
  json,
  mockError,
  numberField,
  readObject,
  stringField,
} from "./http";
import type { MockState } from "./state";
import { mockNow } from "./state";

const SETTINGS_PATH = ["settings", "github"];

const handleGitHubRepositoryReads = (
  request: Request,
  state: MockState,
  segments: string[]
): Response | undefined => {
  if (request.method !== "GET") {
    return undefined;
  }
  if (
    segments.length === 3 &&
    segments[0] === "settings" &&
    segments[1] === "github" &&
    segments[2] === "repositories"
  ) {
    return state.githubAppSettings.configured
      ? json({ repositories: state.githubRepositories })
      : mockError(
          "github_app_not_configured",
          "Configure the GitHub App before listing repositories.",
          409
        );
  }
  if (
    segments.length === 5 &&
    segments[0] === "settings" &&
    segments[1] === "github" &&
    segments[2] === "repositories" &&
    segments[4] === "paths"
  ) {
    const parameters = new URL(request.url).searchParams;
    const query = parameters.get("q")?.trim().toLowerCase() ?? "";
    const dockerfilesOnly = parameters.get("kind") === "dockerfile";
    const paths = [
      { path: "Dockerfile", type: "blob" },
      { path: "apps", type: "tree" },
      { path: "apps/api", type: "tree" },
      { path: "apps/api/Dockerfile", type: "blob" },
      { path: "packages/shared", type: "tree" },
    ].filter(
      (item) =>
        (!dockerfilesOnly ||
          (item.type === "blob" &&
            item.path.toLowerCase().includes("dockerfile"))) &&
        (!query || item.path.toLowerCase().includes(query))
    );
    return json({
      paths,
    });
  }
  return undefined;
};

export const handleGitHubAPI = async (
  request: Request,
  state: MockState,
  pathname: string
): Promise<Response | undefined> => {
  const segments = apiSegments(pathname);
  const isSettings =
    segments.length === SETTINGS_PATH.length &&
    segments.every((segment, index) => segment === SETTINGS_PATH[index]);

  if (isSettings && request.method === "GET") {
    return json(state.githubAppSettings);
  }

  if (isSettings && request.method === "PUT") {
    const input = await readObject(request);
    const appId = numberField(input, "appId", 0);
    const privateKey = stringField(input, "privateKeyPem");
    const webhookSecret = stringField(input, "webhookSecret");
    if (appId < 1 || !privateKey || webhookSecret.length < 16) {
      return mockError(
        "invalid_github_app",
        "App ID, private key, and a webhook secret of at least 16 characters are required."
      );
    }
    state.githubAppSettings = {
      appId,
      appSlug: state.githubAppSettings.appSlug || "platformd-mock",
      configured: true,
      updatedAt: mockNow(),
      webhookPath: "/api/v1/integrations/github/webhook",
    };
    return json(state.githubAppSettings);
  }

  return handleGitHubRepositoryReads(request, state, segments);
};
