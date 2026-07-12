import { Check, Copy, Database, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { createManagedPostgres, fetchManagedImageTags } from "@/api";
import type { ManagedPostgres } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

interface PostgresCreatePanelProperties {
  onClose: () => void;
  onCreated: () => void;
  projectID: string;
}

export const PostgresCreatePanel = ({
  onClose,
  onCreated,
  projectID,
}: PostgresCreatePanelProperties) => {
  const [name, setName] = useState("");
  const [imageTag, setImageTag] = useState("");
  const [cpu, setCPU] = useState("");
  const [memoryMiB, setMemoryMiB] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [created, setCreated] = useState<ManagedPostgres | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const loadTags = async () => {
      try {
        const page = await fetchManagedImageTags(
          "postgres",
          { pageSize: 50 },
          controller.signal
        );
        setTags(page.tags.map((tag) => tag.name));
      } catch {
        // Manual official tags remain available when Docker Hub is unavailable.
      }
    };
    void loadTags();
    return () => controller.abort();
  }, []);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    setSaving(true);
    setError(null);
    try {
      setCreated(
        await createManagedPostgres(projectID, {
          cpuMillicores: cpu === "" ? undefined : Number(cpu),
          imageTag,
          memoryBytes:
            memoryMiB === "" ? undefined : Number(memoryMiB) * 1024 * 1024,
          name,
        })
      );
    } catch (createError) {
      setError(
        createError instanceof Error
          ? createError.message
          : "Unable to create managed PostgreSQL"
      );
    } finally {
      setSaving(false);
    }
  };

  const connectionURL = created
    ? `postgres://${created.ownerUsername}:${created.ownerPassword ?? ""}@${created.hostname}:${created.port}/${created.databaseName}`
    : "";

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-[-8px_0_24px_oklch(0_0_0/5%)]">
      <div className="flex h-12 items-center border-b border-border px-4">
        <Database className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">New PostgreSQL</h2>
        <Button
          aria-label="Close PostgreSQL form"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>
      {created ? (
        <div className="px-4 py-5">
          <div className="flex items-center gap-2 text-xs font-medium">
            <Check className="size-4 text-emerald-500" />
            PostgreSQL desired state created
          </div>
          <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
            Save this owner connection URL now. The password is not revealed
            again.
          </p>
          <code className="mt-5 block bg-muted px-3 py-3 text-[10px] break-all select-all">
            {connectionURL}
          </code>
          <Button
            className="mt-3 w-full"
            onClick={() => {
              void navigator.clipboard.writeText(connectionURL);
              setCopied(true);
            }}
            variant="outline"
          >
            {copied ? <Check /> : <Copy />}
            {copied ? "Copied" : "Copy connection URL"}
          </Button>
          <Button className="mt-3 w-full" onClick={onCreated}>
            I saved the credentials
          </Button>
        </div>
      ) : (
        <form className="px-4 py-5" onSubmit={submit}>
          <FormField label="Resource name" name="postgres-name">
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="postgres-name"
              onChange={(event) => setName(event.target.value)}
              placeholder="database"
              required
              spellCheck={false}
              value={name}
            />
          </FormField>
          <FormField label="Official PostgreSQL tag" name="postgres-tag">
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="postgres-tag"
              list="postgres-tags"
              onChange={(event) => setImageTag(event.target.value)}
              placeholder="18.3"
              required
              spellCheck={false}
              value={imageTag}
            />
            <datalist id="postgres-tags">
              {tags.map((tag) => (
                <option key={tag} value={tag}>
                  {tag}
                </option>
              ))}
            </datalist>
            <p className="mt-1.5 text-[9px] leading-4 text-muted-foreground">
              Suggestions come directly from the official Docker Hub repository.
            </p>
          </FormField>
          <div className="grid grid-cols-2 gap-3">
            <FormField label="CPU millicores" name="postgres-cpu">
              <Input
                id="postgres-cpu"
                min={1}
                onChange={(event) => setCPU(event.target.value)}
                placeholder="Unlimited"
                type="number"
                value={cpu}
              />
            </FormField>
            <FormField label="Memory MiB" name="postgres-memory">
              <Input
                id="postgres-memory"
                min={1}
                onChange={(event) => setMemoryMiB(event.target.value)}
                placeholder="Unlimited"
                type="number"
                value={memoryMiB}
              />
            </FormField>
          </div>
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
              {saving ? "Creating…" : "Create PostgreSQL"}
            </Button>
          </div>
        </form>
      )}
    </aside>
  );
};
