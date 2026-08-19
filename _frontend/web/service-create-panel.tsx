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
import { ServiceHostPicker } from "@/service-host-picker";

interface ServiceCreatePanelProperties {
  onClose: () => void;
  onDrafted: (input: CreateServiceInput) => void;
}

export const ServiceCreatePanel = ({
  onClose,
  onDrafted,
}: ServiceCreatePanelProperties) => {
  const [name, setName] = useState("");
  const [hostId, setHostId] = useState("");
  const [configuration, setConfiguration] = useState(
    emptyServiceConfigurationDraft
  );
  const [error, setError] = useState<string>();

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(undefined);
    try {
      const parsed = parseServiceConfiguration(configuration);
      onDrafted({
        environment: {},
        healthCheck: parsed.healthCheck,
        hostId: hostId || undefined,
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
          <FormField label="Server" name="service-host">
            <ServiceHostPicker
              id="service-host"
              onChange={setHostId}
              value={hostId}
            />
          </FormField>
        </div>

        <ServiceConfiguration
          draft={configuration}
          onDraftChange={setConfiguration}
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
          <Button type="submit">Add service draft</Button>
        </div>
      </form>
    </aside>
  );
};
