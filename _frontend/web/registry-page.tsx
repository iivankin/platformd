import {
  Check,
  Clipboard,
  Globe2,
  LockKeyhole,
  PackagePlus,
  Plus,
  X,
} from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  createRegistryRepository,
  fetchRegistryRepositories,
  fetchRegistrySettings,
  setRegistryHostname,
} from "@/api";
import type { RegistryRepository } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";
import { RegistryRepositoryDetail } from "@/registry-repository-detail";
import { RegistryRepositoryList } from "@/registry-repository-list";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export const RegistryPage = () => {
  const [hostname, setHostname] = useState("");
  const [hostnameInput, setHostnameInput] = useState("");
  const [repositories, setRepositories] = useState<RegistryRepository[]>([]);
  const [selectedID, setSelectedID] = useState<string>();
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [credentialName, setCredentialName] = useState("deployer");
  const [permission, setPermission] = useState<"pull" | "pull_push">(
    "pull_push"
  );
  const [publicPull, setPublicPull] = useState(false);
  const [revealed, setRevealed] = useState<RegistryRepository>();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [settings, loadedRepositories] = await Promise.all([
          fetchRegistrySettings(controller.signal),
          fetchRegistryRepositories(controller.signal),
        ]);
        setHostname(settings.hostname);
        setHostnameInput(settings.hostname);
        setRepositories(loadedRepositories);
        setSelectedID((current) => current ?? loadedRepositories[0]?.id);
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load Registry"));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const saveHostname = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) {
      return;
    }
    setBusy("hostname");
    setError(undefined);
    try {
      const updated = await setRegistryHostname(hostnameInput.trim());
      setHostname(updated.hostname);
      setHostnameInput(updated.hostname);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to configure Registry hostname"));
    } finally {
      setBusy("");
    }
  };

  const create = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || name.trim() === "" || credentialName.trim() === "") {
      return;
    }
    setBusy("create");
    setError(undefined);
    try {
      const created = await createRegistryRepository({
        credentialName,
        credentialPermission: permission,
        name,
        publicPull,
      });
      setRepositories((current) => [
        { ...created, secret: undefined, username: undefined },
        ...current,
      ]);
      setSelectedID(created.id);
      setRevealed(created);
      setCreating(false);
      setName("");
      setCredentialName("deployer");
      setPermission("pull_push");
      setPublicPull(false);
    } catch (createError) {
      setError(errorText(createError, "Unable to create Registry repository"));
    } finally {
      setBusy("");
    }
  };

  const selected = repositories.find(
    (repository) => repository.id === selectedID
  );
  const reloadRepositories = async () => {
    try {
      setRepositories(await fetchRegistryRepositories());
    } catch (reloadError) {
      setError(
        errorText(reloadError, "Unable to refresh Registry repositories")
      );
    }
  };
  const credential =
    revealed?.username && revealed.secret
      ? `${revealed.username}:${revealed.secret}`
      : "";

  return (
    <div className="enter-row flex min-h-full flex-col">
      <section className="flex min-h-14 items-center justify-between gap-4 border-b border-border px-5 py-3">
        <div>
          <p className="text-xs font-medium">Embedded OCI Registry</p>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Repository-local blobs · OCI and Docker schema 2 · stateless Bearer
            tokens
          </p>
        </div>
        <Button onClick={() => setCreating(true)} size="sm">
          <Plus />
          New repository
        </Button>
      </section>

      <form
        className="grid border-b border-border bg-muted/20 lg:grid-cols-[180px_minmax(16rem,1fr)_auto] lg:items-center"
        onSubmit={saveHostname}
      >
        <div className="flex items-center gap-2 px-5 py-3 text-[10px] font-medium">
          <Globe2 className="size-3.5 text-muted-foreground" />
          Registry hostname
        </div>
        <div className="border-y border-border px-4 py-2 lg:border-x lg:border-y-0">
          <Input
            aria-label="Registry hostname"
            className="h-7 border-0 bg-transparent px-0 font-mono text-[10px] focus-visible:ring-0"
            onChange={(event) => setHostnameInput(event.target.value)}
            placeholder="registry.example.com"
            value={hostnameInput}
          />
        </div>
        <div className="flex gap-2 px-4 py-2">
          <Button
            disabled={Boolean(busy) || hostnameInput === hostname}
            size="sm"
            type="submit"
          >
            {busy === "hostname" ? "Saving…" : "Save"}
          </Button>
          {hostname ? (
            <Button
              disabled={Boolean(busy)}
              onClick={() => setHostnameInput("")}
              size="sm"
              type="button"
              variant="ghost"
            >
              Disable
            </Button>
          ) : null}
        </div>
      </form>

      {revealed && credential ? (
        <section className="border-b border-emerald-500/30 bg-emerald-500/5 px-5 py-4">
          <div className="flex items-start gap-3">
            <Check className="mt-0.5 size-4 shrink-0 text-emerald-600" />
            <div className="min-w-0 flex-1">
              <p className="text-xs font-medium">
                Save the robot credential now
              </p>
              <p className="mt-1 text-[10px] text-muted-foreground">
                The password is HMAC-only in platformd and cannot be displayed
                again.
              </p>
              <code className="mt-3 block overflow-x-auto border border-emerald-500/30 bg-background px-3 py-2 text-[10px] select-all">
                {credential}
              </code>
              {hostname ? (
                <code className="mt-2 block text-[9px] text-muted-foreground">
                  docker login {hostname} -u {revealed.username}{" "}
                  --password-stdin
                </code>
              ) : null}
            </div>
            <Button
              onClick={() => void navigator.clipboard.writeText(credential)}
              size="sm"
              variant="outline"
            >
              <Clipboard />
              Copy
            </Button>
            <Button
              aria-label="Dismiss robot credential"
              onClick={() => setRevealed(undefined)}
              size="icon"
              variant="ghost"
            >
              <X />
            </Button>
          </div>
        </section>
      ) : null}

      {creating ? (
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
            <FormField label="Robot name" name="registry-credential-name">
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
            Anonymous pull
          </label>
          <div className="flex gap-2 px-5 pb-4 lg:pb-5">
            <Button
              onClick={() => setCreating(false)}
              size="sm"
              type="button"
              variant="ghost"
            >
              Cancel
            </Button>
            <Button
              disabled={Boolean(busy) || !name.trim() || !credentialName.trim()}
              size="sm"
              type="submit"
            >
              <PackagePlus />
              Create
            </Button>
          </div>
        </form>
      ) : null}

      {error ? (
        <p
          aria-live="polite"
          className="border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive"
        >
          {error}
        </p>
      ) : null}

      <section className="grid min-h-[36rem] flex-1 lg:grid-cols-[minmax(18rem,0.8fr)_minmax(0,2.2fr)]">
        <RegistryRepositoryList
          onSelect={(repository) => setSelectedID(repository.id)}
          repositories={repositories}
          selectedID={selectedID}
        />
        {selected ? (
          <RegistryRepositoryDetail
            hostname={hostname}
            key={selected.id}
            onDeleted={(repositoryID) => {
              const remaining = repositories.filter(
                (repository) => repository.id !== repositoryID
              );
              setRepositories(remaining);
              setSelectedID(remaining[0]?.id);
            }}
            onChanged={() => void reloadRepositories()}
            repository={selected}
          />
        ) : (
          <div className="grid min-h-80 place-items-center px-8 text-center">
            <div>
              <PackagePlus className="mx-auto size-6 text-muted-foreground" />
              <p className="mt-4 text-xs font-medium">Choose a repository</p>
              <p className="mt-2 text-[10px] text-muted-foreground">
                Images and destructive controls appear here.
              </p>
            </div>
          </div>
        )}
      </section>
    </div>
  );
};
