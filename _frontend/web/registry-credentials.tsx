import { Clipboard, KeyRound, Plus, Trash2 } from "lucide-react";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

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
      setCredentials((current) => [...current, credential]);
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

  return (
    <div className="border-b border-border">
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
          <Select
            items={{ pull: "Pull only", pull_push: "Pull + push" }}
            onValueChange={(value) =>
              setPermission(String(value) as "pull" | "pull_push")
            }
            value={permission}
          >
            <SelectTrigger
              aria-label="Robot credential permission"
              className="h-8 w-full text-[10px]"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="start">
              <SelectItem value="pull_push">Pull + push</SelectItem>
              <SelectItem value="pull">Pull only</SelectItem>
            </SelectContent>
          </Select>
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
      <div>
        {credentials.map((credential) => (
          <section
            className="border-b border-border last:border-b-0"
            key={credential.id}
          >
            <header className="grid min-h-11 grid-cols-[minmax(0,1fr)_100px_160px_32px] items-center gap-3 px-4 text-[9px]">
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
            </header>
            <dl className="border-t border-border bg-muted/10">
              {[
                { label: "Username", value: credential.username },
                ...(credential.secretAvailable && credential.secret
                  ? [
                      { label: "Password", value: credential.secret },
                      {
                        label: "Credential",
                        value: `${credential.username}:${credential.secret}`,
                      },
                      ...(hostname
                        ? [
                            {
                              label: "Docker login",
                              value: `printf '%s' '${credential.secret}' | docker login ${hostname} --username ${credential.username} --password-stdin`,
                            },
                          ]
                        : []),
                    ]
                  : []),
              ].map((detail) => (
                <div
                  className="grid min-h-10 grid-cols-[8rem_minmax(0,1fr)_2rem] items-center border-b border-border last:border-b-0"
                  key={detail.label}
                >
                  <dt className="px-4 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                    {detail.label}
                  </dt>
                  <dd className="min-w-0 border-x border-border px-3 py-2">
                    <code className="block overflow-x-auto text-[9px] whitespace-nowrap select-all">
                      {detail.value}
                    </code>
                  </dd>
                  <dd className="grid place-items-center">
                    <Button
                      aria-label={`Copy ${detail.label}`}
                      onClick={() =>
                        void navigator.clipboard.writeText(detail.value)
                      }
                      size="icon"
                      variant="ghost"
                    >
                      <Clipboard />
                    </Button>
                  </dd>
                </div>
              ))}
              {credential.secretAvailable ? null : (
                <div className="border-t border-amber-500/20 bg-amber-500/5 px-4 py-3 text-[9px] leading-4 text-muted-foreground">
                  This legacy credential remains valid, but its password was
                  created before persistent reveal was available. Create a new
                  credential to get complete connection details.
                </div>
              )}
            </dl>
          </section>
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
    </div>
  );
};
