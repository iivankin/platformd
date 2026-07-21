import {
  Activity,
  Boxes,
  GitBranch,
  LockKeyhole,
  Package,
  Power,
  Settings2,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Link } from "react-router";

import { fetchGitHubAppSettings, fetchGitHubRepositories } from "@/api";
import type {
  CreateServiceInput,
  GitHubRepository,
  Service,
  ServiceRegistryCredential,
  ServiceSource,
} from "@/api";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { githubAppInstallationURL } from "@/github-app";
import { PlatformImageCombobox } from "@/platform-image-combobox";
import { RepositoryPathCombobox } from "@/repository-path-combobox";
import { ServiceRegistryCredentialFields } from "@/service-registry-credential-fields";
import { TriggerPathEditor } from "@/trigger-path-editor";

const configureGitHubAppValue = "__configure_github_app__";

export interface ServiceConfigurationDraft {
  healthEnabled: boolean;
  healthPath: string;
  healthPort: string;
  healthTimeout: string;
  registryCredential: Pick<ServiceRegistryCredential, "password" | "username">;
  source: ServiceSource;
}

export interface ServiceConfigurationValues {
  healthCheck?: Service["healthCheck"];
  registryCredential?: Pick<ServiceRegistryCredential, "password" | "username">;
  source: ServiceSource;
}

const defaultSource = (): ServiceSource => ({
  autoUpdate: true,
  image: { reference: "" },
  type: "public_image",
});

export const emptyServiceConfigurationDraft =
  (): ServiceConfigurationDraft => ({
    healthEnabled: false,
    healthPath: "/health",
    healthPort: "8080",
    healthTimeout: "60",
    registryCredential: { password: "", username: "" },
    source: defaultSource(),
  });

export const serviceConfigurationDraftFromCreateInput = (
  input: CreateServiceInput
): ServiceConfigurationDraft => ({
  healthEnabled: input.healthCheck !== undefined,
  healthPath: input.healthCheck?.path ?? "/health",
  healthPort: String(input.healthCheck?.port ?? 8080),
  healthTimeout: String(input.healthCheck?.timeoutSeconds ?? 60),
  registryCredential: input.registryCredential ?? {
    password: "",
    username: "",
  },
  source: input.source,
});

export const serviceConfigurationDraft = (
  service: Service
): ServiceConfigurationDraft => ({
  healthEnabled: service.healthCheck !== undefined,
  healthPath: service.healthCheck?.path ?? "/health",
  healthPort: service.healthCheck?.port.toString() ?? "8080",
  healthTimeout: service.healthCheck?.timeoutSeconds.toString() ?? "60",
  registryCredential: {
    password: service.registryCredential?.password ?? "",
    username: service.registryCredential?.username ?? "",
  },
  source: service.source ?? defaultSource(),
});

const parseHealthCheck = (
  draft: ServiceConfigurationDraft
): Service["healthCheck"] => {
  if (!draft.healthEnabled) {
    return undefined;
  }
  const port = Number(draft.healthPort);
  const timeoutSeconds = Number(draft.healthTimeout);
  if (!Number.isInteger(port) || port < 1 || port > 65_535) {
    throw new Error("Health check port must be between 1 and 65535");
  }
  if (
    !Number.isInteger(timeoutSeconds) ||
    timeoutSeconds < 1 ||
    timeoutSeconds > 3600
  ) {
    throw new Error("Health check timeout must be between 1 and 3600 seconds");
  }
  if (!draft.healthPath.startsWith("/")) {
    throw new Error("Health check path must start with /");
  }
  return { path: draft.healthPath.trim(), port, timeoutSeconds };
};

const validateServiceSource = (
  draft: ServiceConfigurationDraft,
  httpDomainCount?: number
) => {
  if (draft.source.type === "github") {
    if (
      !draft.source.github.repository.trim() ||
      !draft.source.github.branch.trim()
    ) {
      throw new Error("GitHub repository and branch are required");
    }
    if (
      draft.source.github.pullRequestPreview &&
      httpDomainCount !== undefined &&
      httpDomainCount !== 1
    ) {
      throw new Error("PR previews require exactly one HTTP domain");
    }
  } else if (!draft.source.image.reference.trim()) {
    throw new Error("Image reference is required");
  }
  if (
    draft.source.type === "private_image" &&
    (!draft.registryCredential.username.trim() ||
      !draft.registryCredential.password)
  ) {
    throw new Error("Private registry username and password are required");
  }
};

export const parseServiceConfiguration = (
  draft: ServiceConfigurationDraft,
  httpDomainCount?: number
): ServiceConfigurationValues => {
  validateServiceSource(draft, httpDomainCount);
  const healthCheck = parseHealthCheck(draft);
  return {
    healthCheck,
    registryCredential:
      draft.source.type === "private_image"
        ? draft.registryCredential
        : undefined,
    source: draft.source,
  };
};

const sourceOptions: {
  description: string;
  icon: typeof Package;
  label: string;
  type: ServiceSource["type"];
}[] = [
  {
    description: "Build a Dockerfile after matching GitHub pushes.",
    icon: GitBranch,
    label: "GitHub repository",
    type: "github",
  },
  {
    description: "Use an image stored in the built-in registry.",
    icon: Boxes,
    label: "platformd Registry",
    type: "platformd_registry",
  },
  {
    description: "Pull an image that does not require credentials.",
    icon: Package,
    label: "Public image",
    type: "public_image",
  },
  {
    description: "Pull an image using credentials owned by this service.",
    icon: LockKeyhole,
    label: "Private image",
    type: "private_image",
  },
];

const sourceForType = (
  type: ServiceSource["type"],
  current: ServiceSource
): ServiceSource => {
  if (type === current.type) {
    return current;
  }
  if (type === "github") {
    return {
      github: {
        branch: "main",
        contextPath: ".",
        dockerfilePath: "Dockerfile",
        repository: "",
        repositoryId: 0,
        triggerPaths: [],
        waitForCi: false,
      },
      type,
    };
  }
  if (type === "private_image") {
    return {
      autoUpdate: true,
      image: { reference: "" },
      type,
    };
  }
  return { autoUpdate: true, image: { reference: "" }, type };
};

const ToggleRow = ({
  enabled,
  label,
  onChange,
}: {
  enabled: boolean;
  label: string;
  onChange: (enabled: boolean) => void;
}) => (
  <button
    aria-pressed={enabled}
    className="flex min-h-11 w-full items-center gap-3 border-t border-border px-4 text-left hover:bg-muted/40"
    onClick={() => onChange(!enabled)}
    type="button"
  >
    <span
      className={`grid size-5 place-items-center border ${
        enabled
          ? "border-emerald-500/50 bg-emerald-500/10 text-emerald-600"
          : "border-border text-muted-foreground"
      }`}
    >
      <Power className="size-2.5" />
    </span>
    <span className="text-[9px]">{label}</span>
    <span className="ml-auto text-[9px] text-muted-foreground">
      {enabled ? "On" : "Off"}
    </span>
  </button>
);

const SourceFields = ({
  draft,
  embeddedRegistryHost,
  onRegistryCredentialChange,
  onSourceChange,
  registryCredential,
  httpDomainCount,
}: {
  draft: ServiceSource;
  embeddedRegistryHost: string;
  onRegistryCredentialChange: (
    credential: Pick<ServiceRegistryCredential, "password" | "username">
  ) => void;
  onSourceChange: (source: ServiceSource) => void;
  registryCredential: Pick<ServiceRegistryCredential, "password" | "username">;
  httpDomainCount: number;
}) => {
  const [repositories, setRepositories] = useState<GitHubRepository[]>([]);
  const [gitHubAppSlug, setGitHubAppSlug] = useState("");
  const [repositoryError, setRepositoryError] = useState<string>();
  const [repositoryLoadVersion, setRepositoryLoadVersion] = useState(0);

  useEffect(() => {
    if (draft.type !== "github") {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const [loadedRepositories, settings] = await Promise.all([
          fetchGitHubRepositories(controller.signal),
          fetchGitHubAppSettings(controller.signal),
        ]);
        setRepositories(loadedRepositories);
        setGitHubAppSlug(settings.appSlug);
        setRepositoryError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setRepositoryError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load GitHub repositories"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [draft.type, repositoryLoadVersion]);

  if (draft.type === "github") {
    type GitHubSettings = Extract<ServiceSource, { type: "github" }>["github"];
    const updateGitHub = (values: Partial<GitHubSettings>) =>
      onSourceChange({
        ...draft,
        github: { ...draft.github, ...values },
      });
    const installationURL = githubAppInstallationURL(gitHubAppSlug);
    const repositoryItems = [
      ...repositories.map((repository) => ({
        label: repository.fullName,
        value: String(repository.id),
      })),
      { label: "Configure GitHub App", value: configureGitHubAppValue },
    ];
    return (
      <>
        <div className="grid gap-3 p-4 md:grid-cols-2">
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="service-source-repository"
          >
            Repository
            <Select
              items={repositoryItems}
              onValueChange={(value) => {
                if (value === configureGitHubAppValue) {
                  globalThis.open(
                    installationURL ?? "/settings/github",
                    "_blank",
                    "noopener,noreferrer"
                  );
                  return;
                }
                const repository = repositories.find(
                  (candidate) => candidate.id === Number(value)
                );
                if (repository) {
                  updateGitHub({
                    branch: repository.defaultBranch,
                    repository: repository.fullName,
                    repositoryId: repository.id,
                  });
                }
              }}
              onOpenChange={(open) => {
                if (open) {
                  setRepositoryLoadVersion((version) => version + 1);
                }
              }}
              value={
                draft.github.repositoryId > 0
                  ? String(draft.github.repositoryId)
                  : null
              }
            >
              <SelectTrigger className="w-full" id="service-source-repository">
                <SelectValue placeholder="Select installed repository" />
              </SelectTrigger>
              <SelectContent align="start" alignItemWithTrigger={false}>
                {repositories.map((repository) => (
                  <SelectItem key={repository.id} value={String(repository.id)}>
                    {repository.fullName}
                  </SelectItem>
                ))}
                <SelectItem
                  className="border-t border-border"
                  value={configureGitHubAppValue}
                >
                  <Settings2 className="size-3.5 text-muted-foreground" />
                  Configure GitHub App
                </SelectItem>
              </SelectContent>
            </Select>
          </label>
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="service-source-branch"
          >
            Branch
            <Input
              id="service-source-branch"
              onChange={(event) => updateGitHub({ branch: event.target.value })}
              placeholder="main"
              value={draft.github.branch}
            />
          </label>
          {repositoryError ? (
            <p className="text-[9px] text-destructive md:col-span-2">
              {repositoryError}. Configure the GitHub App in Settings.
            </p>
          ) : null}
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="service-source-dockerfile"
          >
            Dockerfile
            <RepositoryPathCombobox
              branch={draft.github.branch}
              id="service-source-dockerfile"
              kind="dockerfile"
              onChange={(dockerfilePath) => updateGitHub({ dockerfilePath })}
              repositoryID={draft.github.repositoryId}
              value={draft.github.dockerfilePath}
            />
          </label>
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="service-source-context"
          >
            Build context
            <RepositoryPathCombobox
              branch={draft.github.branch}
              id="service-source-context"
              kind="directory"
              onChange={(contextPath) => updateGitHub({ contextPath })}
              placeholder="Select or enter a repository directory"
              repositoryID={draft.github.repositoryId}
              value={draft.github.contextPath}
            />
          </label>
          <TriggerPathEditor
            branch={draft.github.branch}
            onChange={(triggerPaths) => updateGitHub({ triggerPaths })}
            paths={draft.github.triggerPaths}
            repositoryID={draft.github.repositoryId}
          />
        </div>
        <ToggleRow
          enabled={draft.github.waitForCi}
          label="Wait for GitHub CI checks before building"
          onChange={(waitForCi) => updateGitHub({ waitForCi })}
        />
        <div className="border-t border-border">
          <ToggleRow
            enabled={draft.github.pullRequestPreview !== undefined}
            label="Pull request previews"
            onChange={(enabled) =>
              updateGitHub({
                pullRequestPreview: enabled
                  ? {
                      hostnameTemplate:
                        draft.github.pullRequestPreview?.hostnameTemplate ??
                        "preview-{{hash}}.example.com",
                    }
                  : undefined,
              })
            }
          />
          {draft.github.pullRequestPreview ? (
            <div className="grid gap-2 border-t border-border px-5 py-4">
              <label
                className="grid gap-1.5 text-[9px] text-muted-foreground"
                htmlFor="service-preview-hostname"
              >
                Preview hostname template
                <Input
                  autoCapitalize="none"
                  autoComplete="off"
                  id="service-preview-hostname"
                  onChange={(event) =>
                    updateGitHub({
                      pullRequestPreview: {
                        hostnameTemplate: event.target.value,
                      },
                    })
                  }
                  placeholder="preview-{{hash}}.example.com"
                  spellCheck={false}
                  value={draft.github.pullRequestPreview.hostnameTemplate}
                />
              </label>
              <p
                className={`text-[9px] leading-4 ${
                  httpDomainCount === 1
                    ? "text-muted-foreground"
                    : "text-destructive"
                }`}
              >
                Requires exactly one HTTP domain. Each commit gets an isolated
                deployment without production volumes; it expires after 14 days.
                GitHub receives a transient deployment and one updated PR
                comment.
              </p>
              <p className="text-[9px] leading-4 text-muted-foreground">
                The hostname must be covered by an origin certificate and a
                scoped Cloudflare DNS token.{" "}
                <Link
                  className="text-foreground underline underline-offset-4"
                  to="/settings/cloudflare"
                >
                  Configure Cloudflare
                </Link>
              </p>
            </div>
          ) : null}
        </div>
      </>
    );
  }

  const updateImage = (reference: string) => {
    if (draft.type === "private_image") {
      onSourceChange({
        ...draft,
        image: { ...draft.image, reference },
      });
      return;
    }
    onSourceChange({ ...draft, image: { reference } });
  };
  return (
    <>
      <div className="grid gap-3 p-4">
        <label
          className="grid gap-1.5 text-[9px] text-muted-foreground"
          htmlFor="service-source-image"
        >
          Image reference
          {draft.type === "platformd_registry" ? (
            <PlatformImageCombobox
              hostname={embeddedRegistryHost}
              id="service-source-image"
              onChange={updateImage}
              value={draft.image.reference}
            />
          ) : (
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="service-source-image"
              onChange={(event) => updateImage(event.target.value)}
              placeholder="ghcr.io/acme/api:latest"
              spellCheck={false}
              value={draft.image.reference}
            />
          )}
        </label>
        {draft.type === "private_image" ? (
          <ServiceRegistryCredentialFields
            imageReference={draft.image.reference}
            onChange={onRegistryCredentialChange}
            password={registryCredential.password}
            username={registryCredential.username}
          />
        ) : null}
      </div>
      <ToggleRow
        enabled={draft.autoUpdate}
        label="Automatically deploy new image digests for this tag"
        onChange={(autoUpdate) => onSourceChange({ ...draft, autoUpdate })}
      />
    </>
  );
};

export const ServiceConfiguration = ({
  draft,
  embeddedRegistryHost,
  onDraftChange,
  httpDomainCount = 0,
}: {
  draft: ServiceConfigurationDraft;
  embeddedRegistryHost: string;
  onDraftChange: (draft: ServiceConfigurationDraft) => void;
  httpDomainCount?: number;
}) => {
  const update = (values: Partial<ServiceConfigurationDraft>) =>
    onDraftChange({ ...draft, ...values });

  return (
    <>
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Source
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Choose how platformd obtains the image for each deployment.
          </p>
        </div>
        <div className="border-t border-border lg:border-t-0 lg:border-l">
          <div className="grid sm:grid-cols-2">
            {sourceOptions.map((option) => {
              const Icon = option.icon;
              const selected = draft.source.type === option.type;
              return (
                <button
                  aria-pressed={selected}
                  className={`flex min-h-16 items-start gap-3 border-b border-border px-4 py-3 text-left sm:odd:border-r ${
                    selected ? "bg-muted/60" : "hover:bg-muted/30"
                  }`}
                  key={option.type}
                  onClick={() =>
                    update({
                      source: sourceForType(option.type, draft.source),
                    })
                  }
                  type="button"
                >
                  <Icon className="mt-0.5 size-3.5 text-muted-foreground" />
                  <span>
                    <span className="block text-[10px] font-medium">
                      {option.label}
                    </span>
                    <span className="mt-1 block text-[8px] leading-3.5 text-muted-foreground">
                      {option.description}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
          <SourceFields
            draft={draft.source}
            embeddedRegistryHost={embeddedRegistryHost}
            onRegistryCredentialChange={(registryCredential) =>
              update({ registryCredential })
            }
            onSourceChange={(source) => update({ source })}
            registryCredential={draft.registryCredential}
            httpDomainCount={httpDomainCount}
          />
        </div>
      </SectionCard>

      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="flex items-center gap-2 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            <Activity className="size-3" /> Health check
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Optional HTTP readiness probe. Off by default.
          </p>
        </div>
        <div className="border-t border-border lg:border-t-0 lg:border-l">
          <ToggleRow
            enabled={draft.healthEnabled}
            label="HTTP health check"
            onChange={(healthEnabled) => update({ healthEnabled })}
          />
          {draft.healthEnabled ? (
            <div className="grid gap-3 px-5 py-4 md:grid-cols-[8rem_minmax(12rem,1fr)_8rem]">
              <label
                className="grid gap-1.5 text-[9px] text-muted-foreground"
                htmlFor="service-health-port"
              >
                Port
                <Input
                  id="service-health-port"
                  max={65_535}
                  min={1}
                  onChange={(event) =>
                    update({ healthPort: event.target.value })
                  }
                  type="number"
                  value={draft.healthPort}
                />
              </label>
              <label
                className="grid gap-1.5 text-[9px] text-muted-foreground"
                htmlFor="service-health-path"
              >
                HTTP path
                <Input
                  id="service-health-path"
                  onChange={(event) =>
                    update({ healthPath: event.target.value })
                  }
                  placeholder="/health"
                  value={draft.healthPath}
                />
              </label>
              <label
                className="grid gap-1.5 text-[9px] text-muted-foreground"
                htmlFor="service-health-timeout"
              >
                Timeout, sec
                <Input
                  id="service-health-timeout"
                  max={3600}
                  min={1}
                  onChange={(event) =>
                    update({ healthTimeout: event.target.value })
                  }
                  type="number"
                  value={draft.healthTimeout}
                />
              </label>
            </div>
          ) : null}
        </div>
      </SectionCard>
    </>
  );
};
