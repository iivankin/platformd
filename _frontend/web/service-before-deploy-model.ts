import type { BeforeDeploy } from "@/api";

export interface BeforeDeployDraft {
  cloudflareEnabled: boolean;
  cloudflareHostnames: string[];
  command: string;
  commandEnabled: boolean;
}

export const emptyBeforeDeployDraft = (): BeforeDeployDraft => ({
  cloudflareEnabled: false,
  cloudflareHostnames: [],
  command: "",
  commandEnabled: false,
});

export const beforeDeployDraft = (
  configuration?: BeforeDeploy
): BeforeDeployDraft => ({
  cloudflareEnabled: Boolean(configuration?.cloudflareHostnames.length),
  cloudflareHostnames: configuration?.cloudflareHostnames ?? [],
  command: configuration?.command ?? "",
  commandEnabled: Boolean(configuration?.command),
});

export const parseBeforeDeploy = (
  draft: BeforeDeployDraft,
  domains: readonly { hostname: string }[]
): BeforeDeploy | undefined => {
  const command = draft.command.trim();
  if (draft.commandEnabled && !command) {
    throw new Error("Before-deploy command is required when enabled");
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
  if (!(draft.commandEnabled || cloudflareHostnames.length)) {
    return undefined;
  }
  return {
    cloudflareHostnames,
    command: draft.commandEnabled ? command : undefined,
  };
};

export const comparableBeforeDeployDraft = (draft: BeforeDeployDraft) => draft;
