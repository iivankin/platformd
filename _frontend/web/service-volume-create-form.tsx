import { useState } from "react";

import type { CreateVolumeInput } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

interface ServiceVolumeCreateFormProperties {
  existingNames: string[];
  onCancel: () => void;
  onCreated: (volume: CreateVolumeInput) => void;
}

export const ServiceVolumeCreateForm = ({
  existingNames,
  onCancel,
  onCreated,
}: ServiceVolumeCreateFormProperties) => {
  const [name, setName] = useState("");
  const [error, setError] = useState<string>();

  const create = () => {
    if (existingNames.includes(name.trim())) {
      setError("A volume with this name already exists.");
      return;
    }
    setError(undefined);
    onCreated({ name: name.trim() });
  };

  return (
    <div className="border-t border-border bg-muted/20 px-4 py-4">
      <FormField label="Volume name" name="new-volume-name">
        <Input
          id="new-volume-name"
          onChange={(event) => setName(event.target.value)}
          placeholder="data"
          value={name}
        />
      </FormField>
      <div className="mt-3 flex gap-2">
        <Button disabled={!name.trim()} onClick={create} size="sm">
          Add volume
        </Button>
        <Button onClick={onCancel} size="sm" variant="ghost">
          Cancel
        </Button>
      </div>
      {error ? (
        <p aria-live="polite" className="mt-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
    </div>
  );
};
