import {
  Check,
  Clipboard,
  ExternalLink,
  FileJson,
  PlugZap,
  ShieldCheck,
} from "lucide-react";
import { useState } from "react";

import type { Project } from "@/api";
import { APITokenCreateDialog } from "@/api-token-create-dialog";
import { APITokensPage } from "@/api-tokens-page";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
import { cn } from "@/lib/utils";
import { mcpClients } from "@/mcp-client-config";
import type { MCPClientID } from "@/mcp-client-config";
import { HighlightedSnippet } from "@/snippet-code";

type CopiedField = "endpoint" | "openapi" | "snippet";

const CopyButton = ({
  copied,
  label,
  onClick,
}: {
  copied: boolean;
  label: string;
  onClick: () => void;
}) => (
  <Button aria-label={label} onClick={onClick} size="sm" variant="outline">
    {copied ? <Check /> : <Clipboard />}
    {copied ? "Copied" : "Copy"}
  </Button>
);

export const SettingsMCPPage = ({ projects }: { projects: Project[] }) => {
  const [clientID, setClientID] = useState<MCPClientID>("codex");
  const [copied, setCopied] = useState<CopiedField>();
  const [copyError, setCopyError] = useState<string>();
  const endpoint = `${window.location.origin}/public/mcp`;
  const openAPIURL = `${window.location.origin}/public/api/v1/openapi.json`;
  const client =
    mcpClients.find((candidate) => candidate.id === clientID) ?? mcpClients[0];
  const snippet = client.snippet(endpoint);

  const copy = async (value: string, field: CopiedField) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(field);
      setCopyError(undefined);
    } catch {
      setCopyError(
        "Clipboard access failed. Select and copy the value manually."
      );
    }
  };

  return (
    <PageStack className="animate-in duration-200 fade-in slide-in-from-bottom-1">
      <SectionCard>
        <div className="grid gap-4 px-5 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
          <div className="max-w-2xl">
            <div className="flex items-center gap-2 text-xs font-medium">
              <PlugZap className="size-4 text-muted-foreground" />
              Connect an MCP client
            </div>
            <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
              Use platformd tools from Codex, Claude, or another Streamable HTTP
              client. The same scoped API tokens authorize REST and MCP.
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-[10px] text-muted-foreground">
            <span className="size-1.5 bg-emerald-500" />
            <span>Server active</span>
            <span className="border-l border-border pl-2">
              2025-06-18 · 2025-11-25
            </span>
          </div>
        </div>

        <div className="grid border-t border-border md:grid-cols-3">
          <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
            <div className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              01 · Create a token
            </div>
            <p className="mt-2 text-[10px] leading-4">
              Choose read or admin access and optionally restrict it to one
              project.
            </p>
            <APITokenCreateDialog projects={projects} />
          </div>
          <div className="border-b border-border px-5 py-4 md:border-r md:border-b-0">
            <div className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              02 · Add the server
            </div>
            <p className="mt-2 text-[10px] leading-4">
              Select your client below, copy its configuration, and replace the
              token placeholder where shown.
            </p>
          </div>
          <div className="px-5 py-4">
            <div className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              03 · Verify tools
            </div>
            <p className="mt-2 text-[10px] leading-4">
              Restart the client, open its MCP server list, and confirm
              platformd tools are enabled.
            </p>
          </div>
        </div>
      </SectionCard>

      <SectionCard>
        <div className="grid border-b border-border lg:grid-cols-[170px_minmax(0,1fr)_auto] lg:items-center">
          <div className="px-5 py-3 text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
            MCP endpoint
          </div>
          <code className="overflow-x-auto border-y border-border bg-background px-4 py-3 text-[10px] select-all lg:border-x lg:border-y-0">
            {endpoint}
          </code>
          <div className="px-4 py-2.5">
            <CopyButton
              copied={copied === "endpoint"}
              label="Copy MCP endpoint"
              onClick={() => void copy(endpoint, "endpoint")}
            />
          </div>
        </div>

        <div className="flex flex-wrap gap-1.5 border-b border-border p-3">
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
              onClick={() => {
                setClientID(candidate.id);
                setCopied(undefined);
              }}
              type="button"
            >
              {candidate.label}
            </button>
          ))}
        </div>

        <div className="grid gap-3 px-5 py-4">
          <p className="text-[10px] leading-4 text-muted-foreground">
            {client.hint}
          </p>
          <div className="relative min-w-0">
            <HighlightedSnippet
              className="bg-secondary/30 p-3 pr-24"
              language={client.language}
              value={snippet}
            />
            <div className="absolute top-2 right-2">
              <CopyButton
                copied={copied === "snippet"}
                label={`Copy ${client.label} MCP configuration`}
                onClick={() => void copy(snippet, "snippet")}
              />
            </div>
          </div>
          <div className="flex items-start gap-2 text-[9px] leading-4 text-muted-foreground">
            <ShieldCheck className="mt-0.5 size-3.5 shrink-0 text-amber-500" />
            <span>
              Tokens are shown only once. Store them in an environment variable
              when your client supports it; never commit them to a repository.
            </span>
          </div>
        </div>
      </SectionCard>

      <SectionCard className="grid lg:grid-cols-[220px_minmax(0,1fr)_auto] lg:items-center">
        <div className="px-5 py-4">
          <div className="flex items-center gap-2 text-xs font-medium">
            <FileJson className="size-4 text-muted-foreground" /> OpenAPI 3.1
          </div>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            Machine-readable REST API contract. Bearer token required.
          </p>
        </div>
        <a
          className="group flex min-w-0 items-center gap-2 border-y border-border px-4 py-3 hover:bg-muted/40 lg:border-x lg:border-y-0"
          href={openAPIURL}
          rel="noreferrer"
          target="_blank"
        >
          <code className="min-w-0 flex-1 truncate text-[10px]">
            {openAPIURL}
          </code>
          <ExternalLink className="size-3.5 shrink-0 text-muted-foreground group-hover:text-foreground" />
        </a>
        <div className="px-4 py-2.5">
          <CopyButton
            copied={copied === "openapi"}
            label="Copy OpenAPI URL"
            onClick={() => void copy(openAPIURL, "openapi")}
          />
        </div>
      </SectionCard>

      {copyError ? (
        <p aria-live="polite" className="text-[10px] text-destructive">
          {copyError}
        </p>
      ) : null}

      <APITokensPage projects={projects} />
    </PageStack>
  );
};
