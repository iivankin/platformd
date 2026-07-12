import { useState } from "react";

import { createImageCredential } from "@/api";
import type { ImageCredential } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

export const ImageCredentialForm = ({
  onCreated,
  projectID,
}: {
  onCreated: (credential: ImageCredential) => void;
  projectID: string;
}) => {
  const [name, setName] = useState("");
  const [registryHost, setRegistryHost] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    if (saving) {
      return;
    }
    if (!(name.trim() && registryHost.trim() && username.trim() && password)) {
      setError("All credential fields are required");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      onCreated(
        await createImageCredential(projectID, {
          name,
          password,
          registryHost,
          username,
        })
      );
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to create image credential"
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="mb-4 border-y border-border bg-muted/20 py-4">
      <p className="mb-3 text-[10px] font-medium">Private remote registry</p>
      <div className="grid grid-cols-2 gap-3">
        <FormField label="Credential name" name="credential-name">
          <Input
            id="credential-name"
            onChange={(event) => setName(event.target.value)}
            placeholder="production"
            value={name}
          />
        </FormField>
        <FormField label="Registry host" name="credential-host">
          <Input
            autoCapitalize="none"
            id="credential-host"
            onChange={(event) => setRegistryHost(event.target.value)}
            placeholder="registry.example.com"
            spellCheck={false}
            value={registryHost}
          />
        </FormField>
        <FormField label="Username" name="credential-username">
          <Input
            id="credential-username"
            onChange={(event) => setUsername(event.target.value)}
            value={username}
          />
        </FormField>
        <FormField label="Password" name="credential-password">
          <Input
            autoComplete="new-password"
            id="credential-password"
            onChange={(event) => setPassword(event.target.value)}
            type="password"
            value={password}
          />
        </FormField>
      </div>
      {error ? (
        <p aria-live="polite" className="text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      <div className="flex justify-end">
        <Button
          disabled={saving}
          onClick={() => void submit()}
          size="sm"
          type="button"
          variant="secondary"
        >
          {saving ? "Saving…" : "Save credential"}
        </Button>
      </div>
    </div>
  );
};
