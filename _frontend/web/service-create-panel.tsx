import { Server, X } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import type { CreateServiceInput } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";
import {
  emptyServiceConfigurationDraft,
  parseServiceConfiguration,
  ServiceConfiguration,
} from "@/service-configuration";

interface ServiceCreatePanelProperties {
  embeddedRegistryHost: string;
  initialDraft?: CreateServiceInput;
  onClose: () => void;
  onDrafted: (input: CreateServiceInput) => void;
}

export const ServiceCreatePanel = ({
  embeddedRegistryHost,
  initialDraft,
  onClose,
  onDrafted,
}: ServiceCreatePanelProperties) => {
  const [name, setName] = useState(initialDraft?.name ?? "");
  const [configuration, setConfiguration] = useState(
    initialDraft
      ? () => ({
          healthEnabled: initialDraft.healthCheck !== undefined,
          healthPath: initialDraft.healthCheck?.path ?? "/health",
          healthPort: String(initialDraft.healthCheck?.port ?? 8080),
          healthTimeout: String(initialDraft.healthCheck?.timeoutSeconds ?? 60),
          registryCredential: initialDraft.registryCredential ?? {
            password: "",
            username: "",
          },
          source: initialDraft.source,
        })
      : emptyServiceConfigurationDraft
  );
  const [error, setError] = useState<string>();

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(undefined);
    try {
      const parsed = parseServiceConfiguration(configuration, 0);
      onDrafted({
        environment: initialDraft?.environment ?? {},
        healthCheck: parsed.healthCheck,
        name,
        registryCredential: parsed.registryCredential,
        source: parsed.source,
      });
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to create service"
      );
    }
  };

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-3xl overflow-y-auto border-l border-border bg-background shadow-lg">
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

      <form className="grid gap-4 p-4" onSubmit={submit}>
        <div className="grid gap-4 border border-border p-4">
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
        </div>

        <ServiceConfiguration
          draft={configuration}
          embeddedRegistryHost={embeddedRegistryHost}
          onDraftChange={setConfiguration}
          httpDomainCount={0}
        />

        {error ? (
          <p aria-live="polite" className="text-[10px] text-destructive">
            {error}
          </p>
        ) : null}
        <div className="flex justify-end gap-2 border-t border-border pt-4">
          <Button onClick={onClose} type="button" variant="ghost">
            Cancel
          </Button>
          <Button type="submit">
            {initialDraft ? "Update draft" : "Add service draft"}
          </Button>
        </div>
      </form>
    </aside>
  );
};
