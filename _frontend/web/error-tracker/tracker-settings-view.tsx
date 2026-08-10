import { KeyRound, PlugZap, ShieldCheck, Trash2 } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { api } from "./api";
import { StatusBadge } from "./common-ui";
import { CreateTokenDialog } from "./create-token-dialog";
import { errorMessage, formatTime } from "./format";
import { mcpClients } from "./mcp-client-config";
import type { MCPClientID } from "./mcp-client-config";
import { CopyButton, SettingsSection } from "./settings-common";
import type { ApiToken, App, Tracker } from "./types";

export const TrackerSettingsView = ({
  apiTokens,
  apps,
  notify,
  refresh,
  tracker,
}: {
  apiTokens: ApiToken[];
  apps: App[];
  notify: (message: string) => void;
  refresh: () => Promise<void>;
  tracker: Tracker;
}) => {
  const [clientID, setClientID] = useState<MCPClientID>("codex");
  const publicBase = tracker.publicUrl.replace(/\/$/u, "");
  const endpoint = `${publicBase}/public/mcp`;
  const restEndpoint = `${publicBase}/public/api/v1`;
  const client =
    mcpClients.find((candidate) => candidate.id === clientID) ?? mcpClients[0];
  const snippet = client.snippet(endpoint);

  const revokeToken = async (tokenId: string) => {
    try {
      await api.revokeToken(tokenId);
      notify("API token revoked");
      await refresh();
    } catch (error) {
      notify(errorMessage(error, "Unable to revoke API token"));
    }
  };

  return (
    <div>
      <section className="border-b border-border">
        <div className="grid gap-4 px-5 py-5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start lg:px-7">
          <div className="max-w-2xl">
            <div className="flex items-center gap-2 text-sm font-medium">
              <PlugZap className="size-4 text-muted-foreground" />
              Connect an MCP client
            </div>
            <p className="mt-1.5 text-[10px] leading-5 text-muted-foreground">
              Investigate issues, events, replays, and artifacts from Codex,
              Claude, or another Streamable HTTP client. Scoped API tokens
              authorize this tracker&apos;s MCP server.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-[9px] text-muted-foreground">
            <span className="size-1.5 bg-emerald-500" />
            <span>Server active</span>
            <span className="border-l border-border pl-2">
              2025-06-18 · 2025-11-25
            </span>
          </div>
        </div>

        <div className="grid border-t border-border md:grid-cols-3">
          <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0 lg:px-7">
            <div className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              01 · Create a token
            </div>
            <p className="mt-2 min-h-10 text-[10px] leading-4">
              Choose read or admin access and optionally restrict it to one
              application.
            </p>
            <div className="mt-3">
              <CreateTokenDialog
                apps={apps}
                defaultAppId=""
                onCreated={refresh}
              />
            </div>
          </div>
          <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0 lg:px-7">
            <div className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              02 · Add the server
            </div>
            <p className="mt-2 text-[10px] leading-4">
              Select your client below, copy its configuration, and provide the
              token through the shown environment variable.
            </p>
          </div>
          <div className="px-5 py-4 lg:px-7">
            <div className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              03 · Verify tools
            </div>
            <p className="mt-2 text-[10px] leading-4">
              Restart the client, open its MCP server list, and confirm the
              error tracker tools are enabled.
            </p>
          </div>
        </div>
      </section>

      <section className="border-b border-border px-5 py-6 lg:px-7">
        <div className="max-w-5xl">
          <div className="grid border-y border-border lg:grid-cols-[136px_minmax(0,1fr)_auto] lg:items-center">
            <div className="px-3 py-3 text-[9px] tracking-[0.1em] text-muted-foreground uppercase">
              MCP endpoint
            </div>
            <code className="overflow-x-auto border-y border-border bg-muted/20 px-4 py-3 text-[10px] select-all lg:border-x lg:border-y-0">
              {endpoint}
            </code>
            <div className="px-3 py-2.5">
              <CopyButton notify={notify} value={endpoint} />
            </div>
          </div>

          <div className="flex flex-wrap gap-1.5 border-b border-border py-3">
            {mcpClients.map((candidate) => (
              <button
                aria-pressed={clientID === candidate.id}
                className={cn(
                  "border px-2.5 py-1 text-[10px] transition-colors",
                  clientID === candidate.id
                    ? "border-amber-500/60 bg-amber-500/10 text-foreground"
                    : "border-border text-muted-foreground hover:border-foreground/20 hover:text-foreground"
                )}
                key={candidate.id}
                onClick={() => setClientID(candidate.id)}
                type="button"
              >
                {candidate.label}
              </button>
            ))}
          </div>

          <div className="grid gap-3 pt-4">
            <p className="text-[10px] leading-4 text-muted-foreground">
              {client.hint}
            </p>
            <div className="relative min-w-0">
              <pre className="overflow-x-auto border border-border bg-muted/20 p-3 pr-24 text-[10px] leading-5 text-foreground">
                {snippet}
              </pre>
              <div className="absolute top-2 right-2">
                <CopyButton key={client.id} notify={notify} value={snippet} />
              </div>
            </div>
            <div className="flex items-start gap-2 text-[9px] leading-4 text-muted-foreground">
              <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-amber-500" />
              <span>
                Tokens are displayed only once. Keep them in an environment
                variable when the client supports it and never commit them.
              </span>
            </div>
          </div>
        </div>
      </section>

      <SettingsSection
        copy="The same scoped bearer credentials authorize public REST clients and MCP. Revoke a token immediately when a client or environment is retired."
        showDivider={false}
        title="API access"
      >
        <div className="grid max-w-4xl grid-cols-[96px_minmax(0,1fr)_auto] items-center gap-3 border-y border-border py-2.5 max-sm:grid-cols-[minmax(0,1fr)_auto]">
          <span className="text-[9px] tracking-[0.08em] text-muted-foreground uppercase max-sm:hidden">
            REST API
          </span>
          <code className="min-w-0 overflow-hidden text-[10px] text-ellipsis whitespace-nowrap text-foreground/75 select-all">
            {restEndpoint}
          </code>
          <div>
            <CopyButton notify={notify} value={restEndpoint} />
          </div>
        </div>

        <div className="mt-5 max-w-4xl">
          <div className="flex items-center justify-between gap-4 py-2">
            <div className="flex items-baseline gap-2">
              <p className="text-[9px] tracking-[0.1em] text-muted-foreground uppercase">
                Issued tokens
              </p>
              <span className="text-[9px] text-muted-foreground">
                {apiTokens.length.toLocaleString()}
              </span>
            </div>
          </div>
          {apiTokens.length === 0 ? (
            <div className="flex min-h-20 items-center gap-2 border-t border-border py-4 text-[10px] text-muted-foreground">
              <KeyRound className="size-3.5" /> No API tokens
            </div>
          ) : (
            apiTokens.map((token) => {
              const scopedApp = apps.find(
                (candidate) => candidate.id === token.appId
              );
              return (
                <div
                  className="grid min-h-14 grid-cols-[minmax(160px,1fr)_80px_minmax(140px,1fr)_32px] items-center gap-3 border-t border-border py-3 max-md:grid-cols-[minmax(0,1fr)_32px]"
                  key={token.id}
                >
                  <div className="min-w-0">
                    <p className="overflow-hidden text-[10px] font-medium text-ellipsis whitespace-nowrap">
                      {token.name}
                    </p>
                    <p className="mt-1 text-[9px] text-muted-foreground">
                      {formatTime(token.createdAt)}
                    </p>
                  </div>
                  <div className="max-md:hidden">
                    <StatusBadge value={token.role} />
                  </div>
                  <span className="overflow-hidden text-[9px] text-ellipsis whitespace-nowrap text-muted-foreground uppercase max-md:hidden">
                    {scopedApp?.name ?? "All applications"}
                  </span>
                  <Button
                    aria-label={`Revoke ${token.name}`}
                    onClick={() => void revokeToken(token.id)}
                    size="icon"
                    variant="ghost"
                  >
                    <Trash2 />
                  </Button>
                </div>
              );
            })
          )}
        </div>
      </SettingsSection>
    </div>
  );
};
