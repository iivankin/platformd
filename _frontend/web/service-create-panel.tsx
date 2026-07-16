import { Activity, Server, X } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { createService } from "@/api";
import type { ImageCredential } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";
import { compatibleImageCredentialID } from "@/image-registry";
import { ImageRegistryAccess } from "@/image-registry-access";
import { parseServiceEnvironment } from "@/service-environment";

interface ServiceCreatePanelProperties {
  credentials: ImageCredential[];
  embeddedRegistryHost: string;
  onClose: () => void;
  onCreated: () => void;
  projectID: string;
}

export const ServiceCreatePanel = ({
  credentials: initialCredentials,
  embeddedRegistryHost,
  onClose,
  onCreated,
  projectID,
}: ServiceCreatePanelProperties) => {
  const [credentials, setCredentials] = useState(initialCredentials);
  const [selectedCredential, setSelectedCredential] = useState("");
  const [name, setName] = useState("");
  const [imageReference, setImageReference] = useState("");
  const [healthEnabled, setHealthEnabled] = useState(false);
  const [healthPort, setHealthPort] = useState("8080");
  const [healthPath, setHealthPath] = useState("/health");
  const [environment, setEnvironment] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submitService = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    setSaving(true);
    setError(null);
    try {
      await createService(projectID, {
        environment: parseServiceEnvironment(environment),
        healthCheck: healthEnabled
          ? {
              path: healthPath,
              port: Number(healthPort),
              timeoutSeconds: 60,
            }
          : undefined,
        imageCredentialId: selectedCredential || undefined,
        imageReference,
        name,
      });
      onCreated();
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to create service"
      );
    } finally {
      setSaving(false);
    }
  };

  const handleCredentialCreated = (credential: ImageCredential) => {
    setCredentials((current) => [...current, credential]);
  };

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-[-8px_0_24px_oklch(0_0_0/5%)]">
      <div className="flex h-12 items-center border-b border-border px-4">
        <Server className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">New service</h2>
        <Button
          aria-label="Close service form"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>

      <form className="px-4 py-5" onSubmit={submitService}>
        <FormField label="Service name" name="service-name">
          <Input
            autoCapitalize="none"
            autoComplete="off"
            id="service-name"
            onChange={(event) => setName(event.target.value)}
            placeholder="api"
            required
            spellCheck={false}
            value={name}
          />
        </FormField>
        <FormField label="OCI image" name="service-image">
          <Input
            autoCapitalize="none"
            autoComplete="off"
            id="service-image"
            onChange={(event) => {
              const nextReference = event.target.value;
              setSelectedCredential((current) =>
                compatibleImageCredentialID(
                  current,
                  credentials,
                  nextReference,
                  embeddedRegistryHost
                )
              );
              setImageReference(nextReference);
            }}
            placeholder="ghcr.io/acme/api:latest"
            required
            spellCheck={false}
            value={imageReference}
          />
        </FormField>
        <div className="mb-5">
          <ImageRegistryAccess
            credentials={credentials}
            embeddedRegistryHost={embeddedRegistryHost}
            id="service-credential"
            imageReference={imageReference}
            onCredentialCreated={handleCredentialCreated}
            onCredentialSelect={setSelectedCredential}
            projectID={projectID}
            selectedCredentialID={selectedCredential}
          />
        </div>

        <section className="mb-5 border-y border-border py-4">
          <button
            aria-pressed={healthEnabled}
            className="flex w-full items-center gap-2 text-left text-[10px]"
            onClick={() => setHealthEnabled((current) => !current)}
            type="button"
          >
            <Activity className="size-3.5 text-muted-foreground" />
            Health check
            <span className="ml-auto text-muted-foreground">
              {healthEnabled ? "Enabled" : "Off"}
            </span>
          </button>
          {healthEnabled ? (
            <div className="mt-4 grid grid-cols-[8rem_minmax(0,1fr)] gap-3">
              <FormField label="Port" name="service-health-port">
                <Input
                  id="service-health-port"
                  max={65_535}
                  min={1}
                  onChange={(event) => setHealthPort(event.target.value)}
                  required
                  type="number"
                  value={healthPort}
                />
              </FormField>
              <FormField label="HTTP path" name="service-health-path">
                <Input
                  id="service-health-path"
                  onChange={(event) => setHealthPath(event.target.value)}
                  required
                  value={healthPath}
                />
              </FormField>
            </div>
          ) : null}
        </section>
        <FormField label="Environment" name="service-environment">
          <textarea
            className="min-h-32 w-full resize-y border border-input bg-background px-2.5 py-2 text-xs leading-5 outline-none placeholder:text-muted-foreground focus:border-ring"
            id="service-environment"
            onChange={(event) => setEnvironment(event.target.value)}
            placeholder={
              "DATABASE_URL=postgres://database:5432/app\nREDIS_URL=redis://cache:6379"
            }
            spellCheck={false}
            value={environment}
          />
        </FormField>
        {error ? (
          <p aria-live="polite" className="mt-4 text-[10px] text-destructive">
            {error}
          </p>
        ) : null}
        <div className="mt-5 flex justify-end gap-2 border-t border-border pt-4">
          <Button onClick={onClose} type="button" variant="ghost">
            Cancel
          </Button>
          <Button disabled={saving} type="submit">
            {saving ? "Creating…" : "Create service"}
          </Button>
        </div>
      </form>
    </aside>
  );
};
