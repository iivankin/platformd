import { Globe2, LockKeyhole, PackagePlus } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { createRegistryRepository } from "@/api";
import type { RegistryRepository } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

export const RegistryRepositoryCreateForm = ({
  onCancel,
  onCreated,
  onError,
}: {
  onCancel: () => void;
  onCreated: (repository: RegistryRepository) => void;
  onError: (message: string) => void;
}) => {
  const [name, setName] = useState("");
  const [credentialName, setCredentialName] = useState("deployer");
  const [permission, setPermission] = useState<"pull" | "pull_push">(
    "pull_push"
  );
  const [publicPull, setPublicPull] = useState(false);
  const [busy, setBusy] = useState(false);

  const create = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || !name.trim() || !credentialName.trim()) {
      return;
    }
    setBusy(true);
    try {
      onCreated(
        await createRegistryRepository({
          credentialName,
          credentialPermission: permission,
          name,
          publicPull,
        })
      );
    } catch (createError) {
      onError(
        createError instanceof Error
          ? createError.message
          : "Unable to create repository"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      className="grid border-b border-border lg:grid-cols-[minmax(12rem,1fr)_minmax(10rem,0.7fr)_150px_150px_auto] lg:items-end"
      onSubmit={create}
    >
      <div className="px-5 pt-4 lg:border-r lg:border-border">
        <FormField label="Repository" name="registry-repository-name">
          <Input
            id="registry-repository-name"
            onChange={(event) => setName(event.target.value)}
            placeholder="team/api"
            value={name}
          />
        </FormField>
      </div>
      <div className="px-5 pt-4 lg:border-r lg:border-border">
        <FormField label="Credential name" name="registry-credential-name">
          <Input
            id="registry-credential-name"
            onChange={(event) => setCredentialName(event.target.value)}
            value={credentialName}
          />
        </FormField>
      </div>
      <div className="px-5 pt-4 lg:border-r lg:border-border">
        <FormField label="Permission" name="registry-permission">
          <select
            className="h-8 w-full border border-input bg-background px-2 text-xs outline-none focus:border-ring"
            id="registry-permission"
            onChange={(event) =>
              setPermission(event.target.value as "pull" | "pull_push")
            }
            value={permission}
          >
            <option value="pull_push">Pull + push</option>
            <option value="pull">Pull only</option>
          </select>
        </FormField>
      </div>
      <label className="flex h-16 items-center gap-2 px-5 text-[10px] lg:border-r lg:border-border">
        <input
          checked={publicPull}
          onChange={(event) => setPublicPull(event.target.checked)}
          type="checkbox"
        />
        {publicPull ? (
          <Globe2 className="size-3.5" />
        ) : (
          <LockKeyhole className="size-3.5" />
        )}
        Public downloads
      </label>
      <div className="flex gap-2 px-5 pb-4 lg:pb-5">
        <Button onClick={onCancel} size="sm" type="button" variant="ghost">
          Cancel
        </Button>
        <Button
          disabled={busy || !name.trim() || !credentialName.trim()}
          size="sm"
          type="submit"
        >
          <PackagePlus /> Create
        </Button>
      </div>
    </form>
  );
};
