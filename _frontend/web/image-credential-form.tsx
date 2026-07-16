import { useState } from "react";

import { createImageCredential } from "@/api";
import type { ImageCredential } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

export const ImageCredentialForm = ({
  onCancel,
  onCreated,
  projectID,
  registryHost,
}: {
  onCancel: () => void;
  onCreated: (credential: ImageCredential) => void;
  projectID: string;
  registryHost: string;
}) => {
  const [name, setName] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    if (saving) {
      return;
    }
    if (!(name.trim() && username.trim() && password)) {
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
    <div className="mt-2 border-t border-border bg-muted/20 py-4">
      <div className="mb-3 flex items-baseline justify-between gap-3">
        <div>
          <p className="text-[10px] font-medium">
            New credential for {registryHost}
          </p>
          <p className="mt-1 text-[9px] text-muted-foreground">
            It will be selected for this service after it is saved.
          </p>
        </div>
      </div>
      <div className="grid gap-3 md:grid-cols-3">
        <FormField label="Credential name" name="credential-name">
          <Input
            id="credential-name"
            onChange={(event) => setName(event.target.value)}
            placeholder="production"
            value={name}
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
      <div className="mt-3 flex justify-end gap-2">
        <Button
          disabled={saving}
          onClick={onCancel}
          size="sm"
          type="button"
          variant="ghost"
        >
          Cancel
        </Button>
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
