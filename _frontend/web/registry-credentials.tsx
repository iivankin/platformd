import { Check, Clipboard, KeyRound, Plus, Trash2, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  createRegistryCredential,
  deleteRegistryCredential,
  fetchRegistryCredentials,
} from "@/api";
import type { RegistryCredential } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const errorText = (error: unknown) =>
  error instanceof Error
    ? error.message
    : "Registry credential operation failed";

export const RegistryCredentials = ({
  hostname,
  repositoryID,
}: {
  hostname: string;
  repositoryID: string;
}) => {
  const [credentials, setCredentials] = useState<RegistryCredential[]>([]);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [permission, setPermission] = useState<"pull" | "pull_push">(
    "pull_push"
  );
  const [revealed, setRevealed] = useState<RegistryCredential>();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setCredentials(
          await fetchRegistryCredentials(repositoryID, controller.signal)
        );
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [repositoryID]);

  const create = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!name.trim() || busy) {
      return;
    }
    setBusy("create");
    setError(undefined);
    try {
      const credential = await createRegistryCredential(repositoryID, {
        name,
        permission,
      });
      setCredentials((current) => [
        ...current,
        { ...credential, secret: undefined, username: undefined },
      ]);
      setRevealed(credential);
      setName("");
      setPermission("pull_push");
      setCreating(false);
    } catch (createError) {
      setError(errorText(createError));
    } finally {
      setBusy("");
    }
  };

  const revoke = async (credentialID: string) => {
    if (busy) {
      return;
    }
    setBusy(credentialID);
    setError(undefined);
    try {
      await deleteRegistryCredential(repositoryID, credentialID);
      setCredentials((current) =>
        current.filter((credential) => credential.id !== credentialID)
      );
    } catch (deleteError) {
      setError(errorText(deleteError));
    } finally {
      setBusy("");
    }
  };

  const secret =
    revealed?.username && revealed.secret
      ? `${revealed.username}:${revealed.secret}`
      : "";

  return (
    <section className="border-b border-border">
      <div className="flex items-center gap-2 border-b border-border px-4 py-2.5">
        <KeyRound className="size-3.5 text-muted-foreground" />
        <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          Robot credentials
        </p>
        <Button
          className="ml-auto"
          onClick={() => setCreating(true)}
          size="sm"
          variant="ghost"
        >
          <Plus />
          Robot
        </Button>
      </div>
      {revealed && secret ? (
        <div className="flex items-start gap-3 border-b border-emerald-500/30 bg-emerald-500/5 px-4 py-3">
          <Check className="mt-0.5 size-3.5 text-emerald-600" />
          <div className="min-w-0 flex-1">
            <p className="text-[10px] font-medium">Save this credential now</p>
            <code className="mt-2 block overflow-x-auto border border-emerald-500/30 bg-background px-2 py-1.5 text-[9px] select-all">
              {secret}
            </code>
            {hostname ? (
              <code className="mt-1 block text-[8px] text-muted-foreground">
                docker login {hostname} -u {revealed.username} --password-stdin
              </code>
            ) : null}
          </div>
          <Button
            aria-label="Copy Registry credential"
            onClick={() => void navigator.clipboard.writeText(secret)}
            size="icon"
            variant="outline"
          >
            <Clipboard />
          </Button>
          <Button
            aria-label="Dismiss Registry credential"
            onClick={() => setRevealed(undefined)}
            size="icon"
            variant="ghost"
          >
            <X />
          </Button>
        </div>
      ) : null}
      {creating ? (
        <form
          className="grid grid-cols-[minmax(10rem,1fr)_150px_auto] items-center gap-2 border-b border-border bg-muted/20 px-4 py-2"
          onSubmit={create}
        >
          <Input
            aria-label="Robot credential name"
            onChange={(event) => setName(event.target.value)}
            placeholder="ci-publisher"
            value={name}
          />
          <select
            aria-label="Robot credential permission"
            className="h-8 border border-input bg-background px-2 text-[10px] outline-none focus:border-ring"
            onChange={(event) =>
              setPermission(event.target.value as "pull" | "pull_push")
            }
            value={permission}
          >
            <option value="pull_push">Pull + push</option>
            <option value="pull">Pull only</option>
          </select>
          <div className="flex gap-1">
            <Button
              onClick={() => setCreating(false)}
              size="sm"
              type="button"
              variant="ghost"
            >
              Cancel
            </Button>
            <Button
              disabled={!name.trim() || Boolean(busy)}
              size="sm"
              type="submit"
            >
              Create
            </Button>
          </div>
        </form>
      ) : null}
      <div className="divide-y divide-border">
        {credentials.map((credential) => (
          <div
            className="grid grid-cols-[minmax(0,1fr)_100px_150px_32px] items-center gap-3 px-4 py-2.5 text-[9px]"
            key={credential.id}
          >
            <span className="truncate font-medium">{credential.name}</span>
            <span className="text-muted-foreground">
              {credential.permission === "pull_push" ? "pull + push" : "pull"}
            </span>
            <span className="text-muted-foreground">
              Last used{" "}
              {credential.lastUsedAt
                ? new Date(credential.lastUsedAt).toLocaleString()
                : "never"}
            </span>
            <Button
              aria-label={`Revoke ${credential.name}`}
              disabled={Boolean(busy)}
              onClick={() => void revoke(credential.id)}
              size="icon"
              variant="ghost"
            >
              <Trash2 />
            </Button>
          </div>
        ))}
      </div>
      {error ? (
        <p
          aria-live="polite"
          className="border-t border-destructive/30 px-4 py-2 text-[9px] text-destructive"
        >
          {error}
        </p>
      ) : null}
    </section>
  );
};
