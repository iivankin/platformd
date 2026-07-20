import { Box, X } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import type { CreateManagedRedisInput } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";
import { ManagedImageTagCombobox } from "@/managed-image-tag-combobox";

type RedisDraftInput = Omit<
  CreateManagedRedisInput,
  "backupPolicy" | "credentials"
>;

interface RedisCreatePanelProperties {
  initialDraft?: RedisDraftInput;
  onClose: () => void;
  onDrafted: (input: RedisDraftInput) => void;
}

export const RedisCreatePanel = ({
  initialDraft,
  onClose,
  onDrafted,
}: RedisCreatePanelProperties) => {
  const [name, setName] = useState(initialDraft?.name ?? "");
  const [imageTag, setImageTag] = useState(initialDraft?.imageTag ?? "");
  const [cpu, setCPU] = useState(initialDraft?.cpuMillicores?.toString() ?? "");
  const [memoryMiB, setMemoryMiB] = useState(
    initialDraft?.memoryBytes
      ? String(initialDraft.memoryBytes / 1024 / 1024)
      : ""
  );
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
        <Box className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">
          {initialDraft ? "Redis draft" : "New Redis"}
        </h2>
        <Button
          aria-label="Close Redis form"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>
      <form className="px-4 py-5" onSubmit={submit}>
        <FormField label="Resource name" name="redis-name">
          <Input
            autoCapitalize="none"
            autoComplete="off"
            id="redis-name"
            onChange={(event) => setName(event.target.value)}
            placeholder="cache"
            required
            spellCheck={false}
            value={name}
          />
        </FormField>
        <FormField label="Official Redis tag" name="redis-tag">
          <ManagedImageTagCombobox
            engine="redis"
            id="redis-tag"
            onChange={setImageTag}
            placeholder="8.2"
            required
            value={imageTag}
          />
          <p className="mt-1.5 text-[9px] leading-4 text-muted-foreground">
            Suggestions come from the official Docker Hub repository.
          </p>
        </FormField>
        <div className="grid grid-cols-2 gap-3">
          <FormField label="CPU millicores" name="redis-cpu">
            <Input
              id="redis-cpu"
              min={1}
              onChange={(event) => setCPU(event.target.value)}
              placeholder="Unlimited"
              type="number"
              value={cpu}
            />
          </FormField>
          <FormField label="Memory MiB" name="redis-memory">
            <Input
              id="redis-memory"
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
            {initialDraft ? "Update draft" : "Add Redis draft"}
          </Button>
        </div>
      </form>
    </aside>
  );
};
