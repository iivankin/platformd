import { Activity, Power } from "lucide-react";

import type { ImageCredential, Service } from "@/api";
import { Input } from "@/components/ui/input";
import {
  compatibleImageCredentialID,
  imageRegistryHost,
  isEmbeddedRegistryReference,
  matchingImageCredentials,
} from "@/image-registry";
import { ImageRegistryAccess } from "@/image-registry-access";

export interface ServiceConfigurationDraft {
  healthEnabled: boolean;
  healthPath: string;
  healthPort: string;
  healthTimeout: string;
  imageCredentialID: string;
  imageReference: string;
}

export interface ServiceConfigurationValues {
  healthCheck?: Service["healthCheck"];
  imageCredentialId?: string;
  imageReference: string;
}

export const serviceConfigurationDraft = (
  service: Service
): ServiceConfigurationDraft => ({
  healthEnabled: service.healthCheck !== undefined,
  healthPath: service.healthCheck?.path ?? "/health",
  healthPort: service.healthCheck?.port.toString() ?? "8080",
  healthTimeout: service.healthCheck?.timeoutSeconds.toString() ?? "60",
  imageCredentialID: service.imageCredentialId ?? "",
  imageReference: service.imageReference,
});

export const parseServiceConfiguration = (
  draft: ServiceConfigurationDraft,
  credentials: ImageCredential[],
  embeddedRegistryHost: string
): ServiceConfigurationValues => {
  const port = Number(draft.healthPort);
  const timeoutSeconds = Number(draft.healthTimeout);
  if (
    draft.healthEnabled &&
    (!Number.isInteger(port) || port < 1 || port > 65_535)
  ) {
    throw new Error("Health check port must be between 1 and 65535");
  }
  if (
    draft.healthEnabled &&
    (!Number.isInteger(timeoutSeconds) ||
      timeoutSeconds < 1 ||
      timeoutSeconds > 3600)
  ) {
    throw new Error("Health check timeout must be between 1 and 3600 seconds");
  }
  if (draft.healthEnabled && !draft.healthPath.startsWith("/")) {
    throw new Error("Health check path must start with /");
  }
  if (!draft.imageReference.trim()) {
    throw new Error("Image reference is required");
  }
  const registryHost = imageRegistryHost(draft.imageReference);
  if (!registryHost) {
    throw new Error("Image reference is invalid");
  }
  if (
    draft.imageCredentialID &&
    isEmbeddedRegistryReference(draft.imageReference, embeddedRegistryHost)
  ) {
    throw new Error("The built-in registry uses automatic authentication");
  }
  if (
    draft.imageCredentialID &&
    !matchingImageCredentials(credentials, draft.imageReference).some(
      (credential) => credential.id === draft.imageCredentialID
    )
  ) {
    throw new Error(`Selected credential is not for ${registryHost}`);
  }
  return {
    healthCheck: draft.healthEnabled
      ? { path: draft.healthPath.trim(), port, timeoutSeconds }
      : undefined,
    imageCredentialId: draft.imageCredentialID || undefined,
    imageReference: draft.imageReference.trim(),
  };
};

export const ServiceConfiguration = ({
  credentials,
  draft,
  embeddedRegistryHost,
  onCredentialCreated,
  onDraftChange,
  projectID,
}: {
  credentials: ImageCredential[];
  draft: ServiceConfigurationDraft;
  embeddedRegistryHost: string;
  onCredentialCreated: (credential: ImageCredential) => void;
  onDraftChange: (draft: ServiceConfigurationDraft) => void;
  projectID: string;
}) => {
  const update = (values: Partial<ServiceConfigurationDraft>) =>
    onDraftChange({ ...draft, ...values });

  return (
    <>
      <section className="grid border-b border-border lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Container image
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Image and registry access used by the next deployment.
          </p>
        </div>
        <div className="grid gap-3 border-t border-border px-5 py-4 lg:border-t-0 lg:border-l">
          <label
            className="grid gap-1.5 text-[9px] text-muted-foreground"
            htmlFor="service-image-reference"
          >
            Image reference
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="service-image-reference"
              onChange={(event) => {
                const imageReference = event.target.value;
                update({
                  imageCredentialID: compatibleImageCredentialID(
                    draft.imageCredentialID,
                    credentials,
                    imageReference,
                    embeddedRegistryHost
                  ),
                  imageReference,
                });
              }}
              required
              spellCheck={false}
              value={draft.imageReference}
            />
          </label>
          <ImageRegistryAccess
            credentials={credentials}
            embeddedRegistryHost={embeddedRegistryHost}
            id="service-image-credential"
            imageReference={draft.imageReference}
            onCredentialCreated={onCredentialCreated}
            onCredentialSelect={(imageCredentialID) =>
              update({ imageCredentialID })
            }
            projectID={projectID}
            selectedCredentialID={draft.imageCredentialID}
          />
        </div>
      </section>

      <section className="grid border-b border-border lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h3 className="flex items-center gap-2 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            <Activity className="size-3" /> Health check
          </h3>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Optional HTTP readiness probe. Off by default.
          </p>
        </div>
        <div className="border-t border-border lg:border-t-0 lg:border-l">
          <button
            aria-pressed={draft.healthEnabled}
            className="flex min-h-12 w-full items-center gap-3 border-b border-border px-5 text-left hover:bg-muted/40"
            onClick={() => update({ healthEnabled: !draft.healthEnabled })}
            type="button"
          >
            <span
              className={`grid size-6 place-items-center border ${
                draft.healthEnabled
                  ? "border-emerald-500/50 bg-emerald-500/10 text-emerald-600"
                  : "border-border text-muted-foreground"
              }`}
            >
              <Power className="size-3" />
            </span>
            <span className="text-[10px]">
              {draft.healthEnabled ? "Enabled" : "Off"}
            </span>
          </button>
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
      </section>
    </>
  );
};
