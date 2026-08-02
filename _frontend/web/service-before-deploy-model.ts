import type { BeforeDeploy, GitHubWorkflow, ServiceSource } from "@/api";

export interface BeforeDeployDraft {
  cloudflareEnabled: boolean;
  cloudflareHostnames: string[];
  command: string;
  commandEnabled: boolean;
  githubInputs: string;
  githubWorkflow?: GitHubWorkflow;
  githubWorkflowEnabled: boolean;
}

export const emptyBeforeDeployDraft = (): BeforeDeployDraft => ({
  cloudflareEnabled: false,
  cloudflareHostnames: [],
  command: "",
  commandEnabled: false,
  githubInputs: "{}",
  githubWorkflowEnabled: false,
});

export const beforeDeployDraft = (
  configuration?: BeforeDeploy
): BeforeDeployDraft => ({
  cloudflareEnabled: Boolean(configuration?.cloudflareHostnames.length),
  cloudflareHostnames: configuration?.cloudflareHostnames ?? [],
  command: configuration?.command ?? "",
  commandEnabled: Boolean(configuration?.command),
  githubInputs: JSON.stringify(
    configuration?.githubWorkflow?.inputs ?? {},
    undefined,
    2
  ),
  githubWorkflow: configuration?.githubWorkflow,
  githubWorkflowEnabled: Boolean(configuration?.githubWorkflow),
});

const parseInputs = (value: string): Record<string, unknown> => {
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch {
    throw new Error("GitHub workflow inputs must be valid JSON");
  }
  if (!parsed || Array.isArray(parsed) || typeof parsed !== "object") {
    throw new Error("GitHub workflow inputs must be a JSON object");
  }
  if (Object.keys(parsed).length > 25) {
    throw new Error("GitHub workflow inputs support at most 25 fields");
  }
  return parsed as Record<string, unknown>;
};

export const parseBeforeDeploy = (
  draft: BeforeDeployDraft,
  source: ServiceSource,
  domains: readonly { hostname: string }[]
): BeforeDeploy | undefined => {
  const command = draft.command.trim();
  if (draft.commandEnabled && !command) {
    throw new Error("Before-deploy command is required when enabled");
  }
  let githubWorkflow: GitHubWorkflow | undefined;
  if (draft.githubWorkflowEnabled) {
    if (source.type !== "github") {
      throw new Error("Before-deploy workflow requires a GitHub source");
    }
    if (!draft.githubWorkflow) {
      throw new Error("Select a GitHub workflow to dispatch");
    }
    githubWorkflow = {
      ...draft.githubWorkflow,
      inputs: parseInputs(draft.githubInputs),
    };
  }
  const allowedHostnames = new Set(domains.map((domain) => domain.hostname));
  const cloudflareHostnames = draft.cloudflareEnabled
    ? [...new Set(draft.cloudflareHostnames)].toSorted()
    : [];
  if (draft.cloudflareEnabled && cloudflareHostnames.length === 0) {
    throw new Error("Select at least one domain for Cloudflare cache purge");
  }
  if (cloudflareHostnames.some((hostname) => !allowedHostnames.has(hostname))) {
    throw new Error("Cloudflare purge domains must be attached to the service");
  }
  if (!(draft.commandEnabled || githubWorkflow || cloudflareHostnames.length)) {
    return undefined;
  }
  return {
    cloudflareHostnames,
    command: draft.commandEnabled ? command : undefined,
    githubWorkflow,
  };
};

export const comparableBeforeDeployDraft = (draft: BeforeDeployDraft) => {
  const { githubInputs: rawGitHubInputs } = draft;
  let githubInputs: unknown = rawGitHubInputs;
  try {
    githubInputs = parseInputs(draft.githubInputs);
  } catch {
    // Keep invalid draft text comparable while the user is editing it.
  }
  return { ...draft, githubInputs };
};
