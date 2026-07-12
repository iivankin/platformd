import {
  Check,
  Clipboard,
  KeyRound,
  Plus,
  ShieldAlert,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import { createAPIToken, fetchAPITokens, revokeAPIToken } from "@/api";
import type { APIToken, Project } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

const formatTime = (value?: number) =>
  value ? new Date(value).toLocaleString() : "Never";

export const APITokensPage = ({ projects }: { projects: Project[] }) => {
  const [tokens, setTokens] = useState<APIToken[]>([]);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [role, setRole] = useState<APIToken["role"]>("read");
  const [projectID, setProjectID] = useState("");
  const [revealed, setRevealed] = useState<APIToken>();
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setTokens(await fetchAPITokens(controller.signal));
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load API tokens"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy || name.trim() === "") {
      return;
    }
    setBusy("create");
    setError(undefined);
    try {
      const created = await createAPIToken({
        name,
        ...(projectID ? { projectId: projectID } : {}),
        role,
      });
      setTokens((current) => [{ ...created, token: undefined }, ...current]);
      setRevealed(created);
      setCopied(false);
      setName("");
      setRole("read");
      setProjectID("");
      setCreating(false);
    } catch (createError) {
      setError(
        createError instanceof Error
          ? createError.message
          : "Unable to create API token"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const revoke = async (token: APIToken) => {
    if (busy) {
      return;
    }
    setBusy(token.id);
    setError(undefined);
    try {
      await revokeAPIToken(token.id);
      setTokens((current) =>
        current.map((candidate) =>
          candidate.id === token.id
            ? { ...candidate, revokedAt: Date.now() }
            : candidate
        )
      );
    } catch (revokeError) {
      setError(
        revokeError instanceof Error
          ? revokeError.message
          : "Unable to revoke API token"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const copy = async () => {
    if (!revealed?.token) {
      return;
    }
    try {
      await navigator.clipboard.writeText(revealed.token);
      setCopied(true);
    } catch {
      setError("Clipboard access failed. Select and copy the token manually.");
    }
  };

  return (
    <div className="enter-row">
      <section className="flex min-h-14 items-center justify-between gap-4 border-b border-border px-5 py-3">
        <div>
          <p className="text-xs font-medium">Automation credentials</p>
          <p className="mt-1 text-[10px] text-muted-foreground">
            REST and MCP · secrets are displayed once
          </p>
        </div>
        <Button onClick={() => setCreating(true)} size="sm">
          <Plus />
          New token
        </Button>
      </section>

      {revealed?.token ? (
        <section className="border-b border-emerald-500/30 bg-emerald-500/5 px-5 py-4">
          <div className="flex items-start gap-3">
            <Check className="mt-0.5 size-4 shrink-0 text-emerald-600" />
            <div className="min-w-0 flex-1">
              <p className="text-xs font-medium">Save this token now</p>
              <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
                The secret cannot be displayed again after this notice closes.
              </p>
              <code className="mt-3 block overflow-x-auto border border-emerald-500/30 bg-background px-3 py-2 text-[10px] leading-5 select-all">
                {revealed.token}
              </code>
            </div>
            <div className="flex shrink-0 gap-1">
              <Button onClick={() => void copy()} size="sm" variant="outline">
                <Clipboard />
                {copied ? "Copied" : "Copy"}
              </Button>
              <Button
                aria-label="Dismiss API token secret"
                onClick={() => setRevealed(undefined)}
                size="icon"
                variant="ghost"
              >
                <X />
              </Button>
            </div>
          </div>
        </section>
      ) : null}

      {creating ? (
        <form
          className="grid border-b border-border bg-muted/20 lg:grid-cols-[1fr_1fr_1fr_auto] lg:items-end"
          onSubmit={submit}
        >
          <div className="px-5 pt-4 lg:border-r lg:border-border">
            <FormField label="Token name" name="token-name">
              <Input
                autoComplete="off"
                id="token-name"
                onChange={(event) => setName(event.target.value)}
                placeholder="deploy-bot"
                value={name}
              />
            </FormField>
          </div>
          <div className="px-5 pt-4 lg:border-r lg:border-border">
            <FormField label="Role" name="token-role">
              <select
                className="h-8 w-full border border-input bg-background px-2 text-xs outline-none focus:border-ring"
                id="token-role"
                onChange={(event) =>
                  setRole(event.target.value as APIToken["role"])
                }
                value={role}
              >
                <option value="read">Read</option>
                <option value="admin">Admin</option>
              </select>
            </FormField>
          </div>
          <div className="px-5 pt-4 lg:border-r lg:border-border">
            <FormField label="Project boundary" name="token-project">
              <select
                className="h-8 w-full border border-input bg-background px-2 text-xs outline-none focus:border-ring"
                id="token-project"
                onChange={(event) => setProjectID(event.target.value)}
                value={projectID}
              >
                <option value="">All projects</option>
                {projects.map((project) => (
                  <option key={project.id} value={project.id}>
                    {project.name}
                  </option>
                ))}
              </select>
            </FormField>
          </div>
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
              disabled={Boolean(busy) || name.trim() === ""}
              size="sm"
              type="submit"
            >
              Create
            </Button>
          </div>
          {role === "admin" && !projectID ? (
            <div className="col-span-full flex items-center gap-2 border-t border-amber-500/30 bg-amber-500/5 px-5 py-2.5 text-[10px] text-amber-800">
              <ShieldAlert className="size-3.5" />
              Unbound admin tokens are full root credentials and can execute
              host commands.
            </div>
          ) : null}
        </form>
      ) : null}

      {error ? (
        <section
          aria-live="polite"
          className="border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive"
        >
          {error}
        </section>
      ) : null}

      <section className="border-b border-border">
        <div className="grid grid-cols-[minmax(180px,1.2fr)_90px_minmax(140px,1fr)_150px_100px_44px] border-b border-border bg-muted/30 px-5 py-2 text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          <span>Name</span>
          <span>Role</span>
          <span>Boundary</span>
          <span>Last used</span>
          <span>Status</span>
          <span />
        </div>
        {tokens.length === 0 ? (
          <div className="grid min-h-64 place-items-center px-8 py-16 text-center">
            <div>
              <KeyRound className="mx-auto size-6 text-muted-foreground" />
              <p className="mt-4 text-xs font-medium">No API tokens</p>
              <p className="mt-2 text-[10px] text-muted-foreground">
                Create one for REST clients or MCP agents.
              </p>
            </div>
          </div>
        ) : (
          tokens.map((token) => {
            const project = projects.find(
              (item) => item.id === token.projectId
            );
            return (
              <div
                className="grid min-h-14 grid-cols-[minmax(180px,1.2fr)_90px_minmax(140px,1fr)_150px_100px_44px] items-center border-b border-border px-5 py-2 last:border-b-0"
                key={token.id}
              >
                <div className="min-w-0">
                  <p className="truncate text-xs font-medium">{token.name}</p>
                  <p className="mt-1 truncate text-[9px] text-muted-foreground">
                    {token.id}
                  </p>
                </div>
                <span className="text-[10px] uppercase">{token.role}</span>
                <span className="truncate text-[10px] text-muted-foreground">
                  {project?.name ?? token.projectId ?? "All projects"}
                </span>
                <span className="text-[10px] text-muted-foreground">
                  {formatTime(token.lastUsedAt)}
                </span>
                <span
                  className={
                    token.revokedAt
                      ? "text-[10px] text-muted-foreground"
                      : "text-[10px] text-emerald-600"
                  }
                >
                  {token.revokedAt ? "Revoked" : "Active"}
                </span>
                <Button
                  aria-label={`Revoke ${token.name}`}
                  disabled={Boolean(token.revokedAt) || Boolean(busy)}
                  onClick={() => void revoke(token)}
                  size="icon"
                  variant="ghost"
                >
                  <Trash2 />
                </Button>
              </div>
            );
          })
        )}
      </section>
    </div>
  );
};
