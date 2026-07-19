import { Database, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { fetchManagedImageTags } from "@/api";
import type { CreateManagedPostgresInput } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

interface PostgresCreatePanelProperties {
  initialDraft?: CreateManagedPostgresInput;
  onClose: () => void;
  onDrafted: (input: CreateManagedPostgresInput) => void;
}

export const PostgresCreatePanel = ({
  initialDraft,
  onClose,
  onDrafted,
}: PostgresCreatePanelProperties) => {
  const [name, setName] = useState(initialDraft?.name ?? "");
  const [imageTag, setImageTag] = useState(initialDraft?.imageTag ?? "");
  const [cpu, setCPU] = useState(initialDraft?.cpuMillicores?.toString() ?? "");
  const [memoryMiB, setMemoryMiB] = useState(
    initialDraft?.memoryBytes
      ? String(initialDraft.memoryBytes / 1024 / 1024)
      : ""
  );
  const [tags, setTags] = useState<string[]>([]);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const page = await fetchManagedImageTags(
          "postgres",
          { pageSize: 50 },
          controller.signal
        );
        setTags(page.tags.map((tag) => tag.name));
      } catch {
        // The field remains editable when Docker Hub suggestions are unavailable.
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onDrafted({
      cpuMillicores: cpu === "" ? undefined : Number(cpu),
      imageTag,
      memoryBytes:
        memoryMiB === "" ? undefined : Number(memoryMiB) * 1024 * 1024,
      name,
    });
  };

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-lg">
      <div className="flex h-12 items-center border-b border-border px-4">
        <Database className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">
          {initialDraft ? "PostgreSQL draft" : "New PostgreSQL"}
        </h2>
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
            Suggestions come from the official Docker Hub repository.
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
        <div className="mt-5 flex justify-end gap-2 border-t border-border pt-4">
          <Button onClick={onClose} type="button" variant="ghost">
            Cancel
          </Button>
          <Button type="submit">
            {initialDraft ? "Update draft" : "Add PostgreSQL draft"}
          </Button>
        </div>
      </form>
    </aside>
  );
};
