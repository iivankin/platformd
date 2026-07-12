import { Box, Check, Copy, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { createManagedRedis, fetchManagedImageTags } from "@/api";
import type { ManagedRedis } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

interface RedisCreatePanelProperties {
  onClose: () => void;
  onCreated: () => void;
  projectID: string;
}

export const RedisCreatePanel = ({
  onClose,
  onCreated,
  projectID,
}: RedisCreatePanelProperties) => {
  const [name, setName] = useState("");
  const [imageTag, setImageTag] = useState("");
  const [cpu, setCPU] = useState("");
  const [memoryMiB, setMemoryMiB] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [created, setCreated] = useState<ManagedRedis | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const loadTags = async () => {
      try {
        const page = await fetchManagedImageTags(
          "redis",
          { pageSize: 50 },
          controller.signal
        );
        setTags(page.tags.map((tag) => tag.name));
      } catch {
        // Manual tag input remains fully supported when Docker Hub is unavailable.
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
        await createManagedRedis(projectID, {
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
          : "Unable to create managed Redis"
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-[-8px_0_24px_oklch(0_0_0/5%)]">
      <div className="flex h-12 items-center border-b border-border px-4">
        <Box className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">New Redis</h2>
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

      {created ? (
        <div className="px-4 py-5">
          <div className="flex items-center gap-2 text-xs font-medium">
            <Check className="size-4 text-emerald-500" />
            Redis desired state created
          </div>
          <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
            Save this password now. Platformd will not reveal it again.
          </p>
          <dl className="mt-5 border-t border-border">
            <div className="border-b border-border py-3">
              <dt className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
                Endpoint
              </dt>
              <dd className="mt-1 text-[10px] break-all">
                {created.hostname}:{created.port}
              </dd>
            </div>
            <div className="border-b border-border py-3">
              <dt className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
                Password
              </dt>
              <dd className="mt-2 flex items-start gap-2">
                <code className="min-w-0 flex-1 bg-muted px-2 py-2 text-[10px] break-all select-all">
                  {created.password}
                </code>
                <Button
                  aria-label="Copy Redis password"
                  onClick={() => {
                    if (created.password) {
                      void navigator.clipboard.writeText(created.password);
                      setCopied(true);
                    }
                  }}
                  size="icon"
                  variant="outline"
                >
                  {copied ? <Check /> : <Copy />}
                </Button>
              </dd>
            </div>
          </dl>
          <Button className="mt-5 w-full" onClick={onCreated}>
            I saved the password
          </Button>
        </div>
      ) : (
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
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="redis-tag"
              list="redis-tags"
              onChange={(event) => setImageTag(event.target.value)}
              placeholder="7.4"
              required
              spellCheck={false}
              value={imageTag}
            />
            <datalist id="redis-tags">
              {tags.map((tag) => (
                <option key={tag} value={tag}>
                  {tag}
                </option>
              ))}
            </datalist>
            <p className="mt-1.5 text-[9px] leading-4 text-muted-foreground">
              Tags are suggestions only; manual official tags are accepted.
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
              {saving ? "Creating…" : "Create Redis"}
            </Button>
          </div>
        </form>
      )}
    </aside>
  );
};
