import { Check, Clipboard, Plus, Server, Trash2, Unplug } from "lucide-react";
import { useEffect, useState } from "react";
import type { FormEvent } from "react";

import {
  createHostJoinToken,
  deleteHost,
  deleteHostJoinToken,
  fetchHostJoinTokens,
  fetchHosts,
} from "@/api";
import type { Host, HostJoinToken } from "@/api";
import { Button } from "@/components/ui/button";
import { FormCard, SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageStack } from "@/components/ui/page-stack";
import { FormField } from "@/form-field";
import { SettingsError } from "@/settings-error";
import { HighlightedSnippet } from "@/snippet-code";

const formatTime = (value?: number) =>
  value ? new Date(value).toLocaleString() : "Never";

const workerInstallCommand =
  "curl -fsSL https://raw.githubusercontent.com/iivankin/platformd/main/install.sh | sudo sh -s -- worker";

const joinCommand = (command: string) =>
  command
    .split("\n")
    .map((line) => line.trim())
    .find((line) => line.includes("platformd join")) ?? command;

const copyText = async (value: string) => {
  await navigator.clipboard.writeText(value);
};

export const SettingsServersPage = () => {
  const [hosts, setHosts] = useState<Host[]>([]);
  const [tokens, setTokens] = useState<HostJoinToken[]>([]);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [revealed, setRevealed] = useState<HostJoinToken>();
  const [copied, setCopied] = useState<"install" | "join">();
  const [busy, setBusy] = useState<string>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [loadedHosts, loadedTokens] = await Promise.all([
          fetchHosts(controller.signal),
          fetchHostJoinTokens(controller.signal),
        ]);
        setHosts(loadedHosts);
        setTokens(loadedTokens);
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
              : "Unable to load child servers"
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
      const created = await createHostJoinToken(name.trim());
      setTokens((current) => [
        {
          createdAt: created.createdAt,
          expiresAt: created.expiresAt,
          id: created.id,
          name: created.name,
        },
        ...current,
      ]);
      setRevealed(created);
      setCopied(undefined);
      setName("");
      setCreating(false);
    } catch (createError) {
      setError(
        createError instanceof Error
          ? createError.message
          : "Unable to create join token"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const removeHost = async (host: Host) => {
    if (busy) {
      return;
    }
    setBusy(host.id);
    setError(undefined);
    try {
      await deleteHost(host.id);
      setHosts((current) =>
        current.filter((candidate) => candidate.id !== host.id)
      );
    } catch (deleteError) {
      setError(
        deleteError instanceof Error
          ? deleteError.message
          : "Unable to delete child server"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const revokeToken = async (token: HostJoinToken) => {
    if (busy) {
      return;
    }
    setBusy(token.id);
    setError(undefined);
    try {
      await deleteHostJoinToken(token.id);
      setTokens((current) =>
        current.filter((candidate) => candidate.id !== token.id)
      );
    } catch (deleteError) {
      setError(
        deleteError instanceof Error
          ? deleteError.message
          : "Unable to revoke join token"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const revealedJoin = revealed?.command
    ? joinCommand(revealed.command)
    : undefined;

  const copy = async (value: string, field: "install" | "join") => {
    try {
      await copyText(value);
      setCopied(field);
    } catch {
      setError(
        "Clipboard access failed. Select and copy the command manually."
      );
    }
  };

  return (
    <PageStack>
      <SettingsError message={error} />

      <SectionCard className="flex min-h-14 items-center justify-between gap-4 px-5 py-3">
        <div>
          <p className="text-xs font-medium">Child servers</p>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Run services on extra VPS hosts. Databases, object storage, and
            previews stay on this installation.
          </p>
        </div>
        <Button onClick={() => setCreating(true)} size="sm">
          <Plus />
          New join token
        </Button>
      </SectionCard>

      {revealed?.command ? (
        <SectionCard className="px-5 py-4">
          <div className="flex items-start gap-3">
            <Check className="mt-0.5 size-4 shrink-0 text-emerald-600" />
            <div className="min-w-0 flex-1">
              <p className="text-xs font-medium">Run this on the new VPS</p>
              <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
                The token is shown once. Install the slim worker, then join.
                After join, attach a proxied Cloudflare A record to the child
                public IPv4 for service hostnames.
              </p>
              <div className="mt-3 flex flex-col gap-3">
                <div className="min-w-0">
                  <p className="text-[10px] text-muted-foreground">
                    Install the worker
                  </p>
                  <HighlightedSnippet
                    className="mt-1 bg-muted/40 px-3 py-2 leading-4 whitespace-pre-wrap"
                    language="bash"
                    value={workerInstallCommand}
                  />
                  <Button
                    className="mt-2"
                    onClick={() => void copy(workerInstallCommand, "install")}
                    size="sm"
                    variant="outline"
                  >
                    {copied === "install" ? <Check /> : <Clipboard />}
                    {copied === "install" ? "Copied" : "Copy install"}
                  </Button>
                </div>
                <div className="min-w-0">
                  <p className="text-[10px] text-muted-foreground">
                    Join this installation
                  </p>
                  <HighlightedSnippet
                    className="mt-1 bg-muted/40 px-3 py-2 leading-4 whitespace-pre-wrap"
                    language="bash"
                    value={revealedJoin ?? ""}
                  />
                  <Button
                    className="mt-2"
                    onClick={() => {
                      if (!revealedJoin) {
                        return;
                      }
                      void copy(revealedJoin, "join");
                    }}
                    size="sm"
                    variant="outline"
                  >
                    {copied === "join" ? <Check /> : <Clipboard />}
                    {copied === "join" ? "Copied" : "Copy join"}
                  </Button>
                </div>
              </div>
              <p className="mt-3 text-[10px] leading-4 text-muted-foreground">
                The installer writes{" "}
                <code className="text-foreground">
                  /usr/local/bin/platformd
                </code>{" "}
                and verifies <code className="text-foreground">SHA256SUMS</code>{" "}
                from the latest{" "}
                <a
                  className="underline underline-offset-2"
                  href="https://github.com/iivankin/platformd/releases"
                  rel="noreferrer"
                  target="_blank"
                >
                  GitHub Release
                </a>
                .
              </p>
              <Button
                className="mt-3"
                onClick={() => setRevealed(undefined)}
                size="sm"
                variant="ghost"
              >
                Dismiss
              </Button>
            </div>
          </div>
        </SectionCard>
      ) : null}

      {creating ? (
        <FormCard
          className="px-5 py-4"
          onSubmit={(event) => void submit(event)}
        >
          <p className="text-xs font-medium">Issue a join token</p>
          <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
            Name becomes the child server label. Tokens expire after 24 hours.
          </p>
          <div className="mt-4 max-w-sm">
            <FormField label="Server name" name="host-name">
              <Input
                autoCapitalize="none"
                autoComplete="off"
                id="host-name"
                onChange={(event) => setName(event.target.value)}
                placeholder="edge-1"
                required
                spellCheck={false}
                value={name}
              />
            </FormField>
          </div>
          <div className="flex gap-2">
            <Button disabled={Boolean(busy)} type="submit">
              Create token
            </Button>
            <Button
              onClick={() => {
                setCreating(false);
                setName("");
              }}
              type="button"
              variant="ghost"
            >
              Cancel
            </Button>
          </div>
        </FormCard>
      ) : null}

      <SectionCard>
        {hosts.length === 0 ? (
          <div className="flex items-start gap-3 px-5 py-4">
            <Server className="mt-0.5 size-4 text-muted-foreground" />
            <div>
              <p className="text-xs font-medium">No child servers yet</p>
              <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
                Services stay on this VPS until a joined host is selected in
                service settings.
              </p>
            </div>
          </div>
        ) : (
          <ul>
            {hosts.map((host) => (
              <li
                className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-3 last:border-b-0"
                key={host.id}
              >
                <div className="min-w-0">
                  <p className="flex items-center gap-2 text-xs font-medium">
                    <span
                      className={
                        host.connected
                          ? "size-1.5 bg-emerald-500"
                          : "size-1.5 bg-muted-foreground"
                      }
                    />
                    {host.name}
                  </p>
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    {host.publicIpv4 ?? "No public IPv4"} ·{" "}
                    {host.connected ? "Connected" : "Offline"} · Last seen{" "}
                    {formatTime(host.lastSeenAt)}
                  </p>
                </div>
                <Button
                  disabled={busy === host.id}
                  onClick={() => void removeHost(host)}
                  size="sm"
                  variant="ghost"
                >
                  <Trash2 />
                  Remove
                </Button>
              </li>
            ))}
          </ul>
        )}
      </SectionCard>

      {tokens.length > 0 ? (
        <SectionCard>
          <div className="border-b border-border px-5 py-3">
            <p className="text-xs font-medium">Unused join tokens</p>
          </div>
          <ul>
            {tokens.map((token) => (
              <li
                className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-3 last:border-b-0"
                key={token.id}
              >
                <div>
                  <p className="text-xs font-medium">{token.name}</p>
                  <p className="mt-1 text-[10px] text-muted-foreground">
                    Expires {formatTime(token.expiresAt)}
                  </p>
                </div>
                <Button
                  disabled={busy === token.id}
                  onClick={() => void revokeToken(token)}
                  size="sm"
                  variant="ghost"
                >
                  <Unplug />
                  Revoke
                </Button>
              </li>
            ))}
          </ul>
        </SectionCard>
      ) : null}
    </PageStack>
  );
};
