import { KeyRound, Server, X } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { createService } from "@/api";
import type { ImageCredential } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";
import { ImageCredentialForm } from "@/image-credential-form";
import { parseServiceEnvironment } from "@/service-environment";

interface ServiceCreatePanelProperties {
  credentials: ImageCredential[];
  onClose: () => void;
  onCreated: () => void;
  projectID: string;
}

export const ServiceCreatePanel = ({
  credentials: initialCredentials,
  onClose,
  onCreated,
  projectID,
}: ServiceCreatePanelProperties) => {
  const [credentials, setCredentials] = useState(initialCredentials);
  const [credentialOpen, setCredentialOpen] = useState(false);
  const [selectedCredential, setSelectedCredential] = useState("");
  const [name, setName] = useState("");
  const [imageReference, setImageReference] = useState("");
  const [targetPort, setTargetPort] = useState("");
  const [healthPath, setHealthPath] = useState("");
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
        healthPath: healthPath || undefined,
        imageCredentialId: selectedCredential || undefined,
        imageReference,
        name,
        targetPort: targetPort === "" ? undefined : Number(targetPort),
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
    setSelectedCredential(credential.id);
    setCredentialOpen(false);
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
            onChange={(event) => setImageReference(event.target.value)}
            placeholder="ghcr.io/acme/api:latest"
            required
            spellCheck={false}
            value={imageReference}
          />
        </FormField>
        <FormField label="Image credential" name="service-credential">
          <div className="flex gap-2">
            <select
              className="h-8 min-w-0 flex-1 border border-input bg-background px-2 text-xs outline-none focus:border-ring"
              id="service-credential"
              onChange={(event) => setSelectedCredential(event.target.value)}
              value={selectedCredential}
            >
              <option value="">Public or embedded registry</option>
              {credentials.map((credential) => (
                <option key={credential.id} value={credential.id}>
                  {credential.name} · {credential.registryHost}
                </option>
              ))}
            </select>
            <Button
              aria-label="Add image credential"
              onClick={() => setCredentialOpen((value) => !value)}
              size="icon"
              type="button"
              variant="outline"
            >
              <KeyRound />
            </Button>
          </div>
        </FormField>

        {credentialOpen ? (
          <ImageCredentialForm
            onCreated={handleCredentialCreated}
            projectID={projectID}
          />
        ) : null}

        <div className="grid grid-cols-2 gap-3">
          <FormField label="Target port" name="service-port">
            <Input
              id="service-port"
              max={65_535}
              min={1}
              onChange={(event) => setTargetPort(event.target.value)}
              placeholder="8080"
              type="number"
              value={targetPort}
            />
          </FormField>
          <FormField label="Health path" name="service-health">
            <Input
              id="service-health"
              onChange={(event) => setHealthPath(event.target.value)}
              placeholder="/healthz"
              value={healthPath}
            />
          </FormField>
        </div>
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
