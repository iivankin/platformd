import { Activity, LockKeyhole, Package, Power, Upload } from "lucide-react";

import type {
  CreateServiceInput,
  Service,
  ServiceRegistryCredential,
  ServiceSource,
} from "@/api";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  GitHubActionExampleDialog,
  uploadImageActionExample,
} from "@/github-action-example-dialog";
import { ServiceRegistryCredentialFields } from "@/service-registry-credential-fields";

const maximumReleaseAgeDays = 36_500;

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
  dockerUpload: { branch: "main", repository: "", workflows: [] },
  type: "docker_image_upload",
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
  healthPort: String(service.healthCheck?.port ?? 8080),
  healthTimeout: String(service.healthCheck?.timeoutSeconds ?? 60),
  registryCredential: {
    password: service.registryCredential?.password ?? "",
    username: service.registryCredential?.username ?? "",
  },
  source: service.source,
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
  const { source } = draft;
  if (source.type === "docker_image_upload") {
    if (
      !/^[a-z0-9][a-z0-9._-]*\/[a-z0-9][a-z0-9._-]*$/u.test(
        source.dockerUpload.repository
      )
    ) {
      throw new Error("Upload repository must be a lowercase owner/name");
    }
    if (!source.dockerUpload.branch.trim()) {
      throw new Error("Production branch is required");
    }
    if (httpDomainCount !== undefined && httpDomainCount !== 1) {
      throw new Error("Image upload services require exactly one HTTP domain");
    }
    return;
  }
  if (source.type === "unconfigured") {
    throw new Error("Select and configure an image source");
  }
  if (!source.image.reference.trim()) {
    throw new Error("Image reference is required");
  }
  if (
    source.minimumReleaseAgeDays !== undefined &&
    (!Number.isInteger(source.minimumReleaseAgeDays) ||
      source.minimumReleaseAgeDays < 1 ||
      source.minimumReleaseAgeDays > maximumReleaseAgeDays)
  ) {
    throw new Error(
      `Minimum release age must be between 1 and ${maximumReleaseAgeDays} days`
    );
  }
  if (
    source.type === "private_image" &&
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
  return {
    healthCheck: parseHealthCheck(draft),
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
  type: Exclude<ServiceSource["type"], "unconfigured">;
}[] = [
  {
    description: "Upload OCI images from GitHub Actions with OIDC.",
    icon: Upload,
    label: "Docker image upload",
    type: "docker_image_upload",
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

const DockerImageUploadFields = ({
  draft,
  httpDomainCount,
  onSourceChange,
  projectID,
  serviceID,
}: {
  draft: Extract<ServiceSource, { type: "docker_image_upload" }>;
  httpDomainCount: number;
  onSourceChange: (source: ServiceSource) => void;
  projectID?: string;
  serviceID?: string;
}) => {
  const update = (values: Partial<typeof draft.dockerUpload>) =>
    onSourceChange({
      ...draft,
      dockerUpload: { ...draft.dockerUpload, ...values },
    });
  const domainReady = httpDomainCount === 1;

  return (
    <div className="grid gap-3 border-t border-border p-4 md:grid-cols-2">
      <label
        className="grid gap-1.5 text-[9px] text-muted-foreground"
        htmlFor="service-upload-repository"
      >
        Repository
        <Input
          autoCapitalize="none"
          autoComplete="off"
          id="service-upload-repository"
          onChange={(event) =>
            update({ repository: event.target.value.toLowerCase().trim() })
          }
          placeholder="org/backend"
          spellCheck={false}
          value={draft.dockerUpload.repository}
        />
      </label>
      <label
        className="grid gap-1.5 text-[9px] text-muted-foreground"
        htmlFor="service-upload-branch"
      >
        Production branch
        <Input
          id="service-upload-branch"
          onChange={(event) => update({ branch: event.target.value })}
          placeholder="main"
          value={draft.dockerUpload.branch}
        />
      </label>
      <label
        className="grid gap-1.5 text-[9px] text-muted-foreground md:col-span-2"
        htmlFor="service-upload-workflows"
      >
        Allowed workflow files · optional
        <Input
          autoCapitalize="none"
          autoComplete="off"
          id="service-upload-workflows"
          onChange={(event) =>
            update({
              workflows: event.target.value
                .split(",")
                .map((value) => value.trim())
                .filter(Boolean),
            })
          }
          placeholder="deploy.yml, release.yaml"
          spellCheck={false}
          value={draft.dockerUpload.workflows.join(", ")}
        />
      </label>
      <div className="flex flex-wrap items-center justify-between gap-3 md:col-span-2">
        <p
          className={`text-[9px] leading-4 ${domainReady ? "text-muted-foreground" : "text-destructive"}`}
        >
          {domainReady
            ? "Ready for image uploads and preview URLs."
            : "Add exactly one HTTP domain before uploading images."}
        </p>
        {projectID && serviceID ? (
          <GitHubActionExampleDialog
            description="GitHub Actions builds an OCI archive and uploads it to this service with OIDC. No registry or docker login."
            example={uploadImageActionExample({ projectID, serviceID })}
            notes={
              <>
                <code>project</code> and <code>resource</code> are this
                project&apos;s and service&apos;s IDs. The workflow needs{" "}
                <code>permissions: id-token: write</code>.
              </>
            }
            steps={[
              "Allow the GitHub repository (and optional workflow files) above.",
              "Keep exactly one HTTP domain so production and preview URLs can be published.",
              "Paste url, project, and resource into the workflow below and run it.",
            ]}
            title="Docker image upload"
          />
        ) : null}
      </div>
    </div>
  );
};

const sourceForType = (
  type: Exclude<ServiceSource["type"], "unconfigured">,
  current: ServiceSource
): ServiceSource => {
  if (type === current.type) {
    return current;
  }
  if (type === "docker_image_upload") {
    return defaultSource();
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
      className={`grid size-5 place-items-center border ${enabled ? "border-emerald-500/50 bg-emerald-500/10 text-emerald-600" : "border-border text-muted-foreground"}`}
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
  httpDomainCount,
  onRegistryCredentialChange,
  onSourceChange,
  projectID,
  registryCredential,
  serviceID,
}: {
  draft: ServiceSource;
  httpDomainCount: number;
  onRegistryCredentialChange: (
    credential: Pick<ServiceRegistryCredential, "password" | "username">
  ) => void;
  onSourceChange: (source: ServiceSource) => void;
  projectID?: string;
  registryCredential: Pick<ServiceRegistryCredential, "password" | "username">;
  serviceID?: string;
}) => {
  if (draft.type === "unconfigured") {
    return (
      <p className="border-t border-border px-4 py-4 text-[9px] text-muted-foreground">
        This migrated service has no source. Select one above.
      </p>
    );
  }
  if (draft.type === "docker_image_upload") {
    return (
      <DockerImageUploadFields
        draft={draft}
        httpDomainCount={httpDomainCount}
        onSourceChange={onSourceChange}
        projectID={projectID}
        serviceID={serviceID}
      />
    );
  }

  const updateImage = (reference: string) =>
    onSourceChange({ ...draft, image: { reference } });
  return (
    <>
      <div className="grid gap-3 border-t border-border p-4">
        <label
          className="grid gap-1.5 text-[9px] text-muted-foreground"
          htmlFor="service-source-image"
        >
          Image reference
          <Input
            autoCapitalize="none"
            autoComplete="off"
            id="service-source-image"
            onChange={(event) => updateImage(event.target.value)}
            placeholder="ghcr.io/acme/api:latest"
            spellCheck={false}
            value={draft.image.reference}
          />
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
      {draft.autoUpdate ? (
        <div className="grid gap-2 border-t border-border px-5 py-4">
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="service-source-minimum-release-age"
          >
            Minimum release age (days)
            <Input
              id="service-source-minimum-release-age"
              inputMode="numeric"
              max={maximumReleaseAgeDays}
              min={1}
              onChange={(event) => {
                const days = event.currentTarget.valueAsNumber;
                onSourceChange({
                  ...draft,
                  minimumReleaseAgeDays: Number.isNaN(days) ? undefined : days,
                });
              }}
              placeholder="Optional"
              step={1}
              type="number"
              value={draft.minimumReleaseAgeDays ?? ""}
            />
          </label>
        </div>
      ) : null}
    </>
  );
};

export const ServiceConfiguration = ({
  draft,
  httpDomainCount = 0,
  onDraftChange,
  projectID,
  serviceID,
}: {
  draft: ServiceConfigurationDraft;
  httpDomainCount?: number;
  onDraftChange: (draft: ServiceConfigurationDraft) => void;
  projectID?: string;
  serviceID?: string;
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
            Choose how platformd obtains the final image.
          </p>
        </div>
        <div className="border-t border-border lg:border-t-0 lg:border-l">
          <div className="grid sm:grid-cols-3">
            {sourceOptions.map((option) => {
              const Icon = option.icon;
              const selected = draft.source.type === option.type;
              return (
                <button
                  aria-pressed={selected}
                  className={`flex min-h-16 items-start gap-3 border-b border-border px-4 py-3 text-left sm:not-last:border-r ${selected ? "bg-muted/60" : "hover:bg-muted/30"}`}
                  key={option.type}
                  onClick={() =>
                    update({ source: sourceForType(option.type, draft.source) })
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
            httpDomainCount={httpDomainCount}
            onRegistryCredentialChange={(registryCredential) =>
              update({ registryCredential })
            }
            onSourceChange={(source) => update({ source })}
            projectID={projectID}
            registryCredential={draft.registryCredential}
            serviceID={serviceID}
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
