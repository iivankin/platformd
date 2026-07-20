import {
  CheckCircle2,
  ExternalLink,
  Eye,
  EyeOff,
  Loader2,
  RefreshCw,
} from "lucide-react";
import { useEffect, useState } from "react";
import type { ReactNode } from "react";

import {
  configureCloudflareMesh,
  fetchCloudflareMeshCredential,
  fetchCloudflareMeshSettings,
  reconnectCloudflareMesh,
} from "@/api";
import type { CloudflareMeshSettings } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const TOKEN_URL = "https://dash.cloudflare.com/profile/api-tokens";

const statusLabel = (settings: CloudflareMeshSettings | undefined) => {
  if (!settings) {
    return "Checking connection…";
  }
  if (settings.status === "connected") {
    return `Connected · ${settings.meshIp}`;
  }
  if (settings.configured) {
    return "Configured · sidecar disconnected";
  }
  return "Not configured";
};

const statusColor = (settings: CloudflareMeshSettings | undefined) =>
  settings?.status === "connected"
    ? "text-emerald-600 dark:text-emerald-400"
    : "text-muted-foreground";

export const CloudflareMeshConnection = ({
  onConfiguredChange,
}: {
  onConfiguredChange?: (configured: boolean) => void;
}) => {
  const [settings, setSettings] = useState<CloudflareMeshSettings>();
  const [accountId, setAccountId] = useState("");
  const [apiToken, setApiToken] = useState("");
  const [editing, setEditing] = useState(false);
  const [revealed, setRevealed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchCloudflareMeshSettings(controller.signal);
        setSettings(loaded);
        setAccountId(loaded.accountId);
        setEditing(!loaded.configured);
        onConfiguredChange?.(loaded.configured);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load Cloudflare Mesh connection"
          );
          onConfiguredChange?.(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [onConfiguredChange]);

  const editCredential = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const credential = await fetchCloudflareMeshCredential();
      setAccountId(credential.accountId);
      setApiToken(credential.apiToken);
      setEditing(true);
    } catch (loadError) {
      setError(
        loadError instanceof Error
          ? loadError.message
          : "Unable to load Cloudflare Mesh credential"
      );
    } finally {
      setBusy(false);
    }
  };

  const configure = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const updated = await configureCloudflareMesh({ accountId, apiToken });
      setSettings(updated);
      setEditing(false);
      setApiToken("");
      setRevealed(false);
      onConfiguredChange?.(updated.configured);
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to configure Cloudflare Mesh"
      );
      onConfiguredChange?.(false);
    } finally {
      setBusy(false);
    }
  };

  const reconnect = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const updated = await reconnectCloudflareMesh();
      setSettings(updated);
      onConfiguredChange?.(updated.configured);
    } catch (connectError) {
      setError(
        connectError instanceof Error
          ? connectError.message
          : "Unable to reconnect Cloudflare Mesh"
      );
    } finally {
      setBusy(false);
    }
  };

  let details: ReactNode = null;
  if (editing) {
    details = (
      <div className="grid gap-4 px-4 py-4 sm:grid-cols-2">
        <div className="sm:col-span-2">
          <p className="text-[9px] leading-4 text-muted-foreground">
            Use an account API token with Cloudflare One Connectors Write.
            Credentials are encrypted in the control backup and reused after
            disaster recovery.
          </p>
          <a
            className="mt-2 inline-flex items-center gap-1 text-[9px] underline underline-offset-4"
            href={TOKEN_URL}
            rel="noreferrer"
            target="_blank"
          >
            Create Cloudflare API token <ExternalLink className="size-3" />
          </a>
        </div>
        <label
          className="grid gap-1.5 text-[9px] text-muted-foreground"
          htmlFor="cloudflare-mesh-account-id"
        >
          Account ID
          <Input
            autoCapitalize="none"
            autoComplete="off"
            id="cloudflare-mesh-account-id"
            maxLength={32}
            minLength={32}
            onChange={(event) => setAccountId(event.target.value)}
            placeholder="32-character Cloudflare account ID"
            spellCheck={false}
            value={accountId}
          />
        </label>
        <label
          className="grid gap-1.5 text-[9px] text-muted-foreground"
          htmlFor="cloudflare-mesh-api-token"
        >
          API token
          <div className="relative">
            <Input
              autoComplete="off"
              className="pr-9"
              id="cloudflare-mesh-api-token"
              minLength={20}
              onChange={(event) => setApiToken(event.target.value)}
              placeholder="Cloudflare API token"
              type={revealed ? "text" : "password"}
              value={apiToken}
            />
            <button
              aria-label={revealed ? "Hide API token" : "Show API token"}
              className="absolute top-1/2 right-2 grid size-6 -translate-y-1/2 place-items-center text-muted-foreground hover:text-foreground"
              onClick={() => setRevealed((current) => !current)}
              type="button"
            >
              {revealed ? (
                <EyeOff className="size-3" />
              ) : (
                <Eye className="size-3" />
              )}
            </button>
          </div>
        </label>
        {error ? (
          <p className="text-[9px] text-destructive sm:col-span-2">{error}</p>
        ) : null}
        <div className="flex justify-end gap-2 border-t border-border pt-4 sm:col-span-2">
          {settings?.configured ? (
            <Button
              disabled={busy}
              onClick={() => {
                setEditing(false);
                setApiToken("");
                setError(undefined);
              }}
              type="button"
              variant="ghost"
            >
              Cancel
            </Button>
          ) : null}
          <Button
            disabled={
              busy ||
              !/^[0-9a-fA-F]{32}$/u.test(accountId.trim()) ||
              apiToken.trim().length < 20
            }
            onClick={() => void configure()}
            type="button"
          >
            {busy ? <Loader2 className="animate-spin" /> : null}
            {busy ? "Installing and connecting…" : "Connect Mesh"}
          </Button>
        </div>
      </div>
    );
  } else if (error) {
    details = <p className="px-4 py-3 text-[9px] text-destructive">{error}</p>;
  }

  return (
    <div className="sm:col-span-2">
      <div className="flex flex-wrap items-start gap-3 border-b border-border px-4 py-4">
        <CheckCircle2
          className={`mt-0.5 size-4 shrink-0 ${statusColor(settings)}`}
        />
        <div className="min-w-0 flex-1">
          <p className="text-[10px] font-medium">Managed Mesh connection</p>
          <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
            platformd creates one Cloudflare Mesh node for this installation,
            runs the official client in an isolated managed container, and keeps
            it connected automatically. Nothing is installed as a host service.
          </p>
          <p className="mt-2 text-[9px] text-muted-foreground">
            {statusLabel(settings)}
            {settings?.nodeName ? ` · ${settings.nodeName}` : ""}
          </p>
        </div>
        {settings?.configured && !editing ? (
          <div className="flex gap-2">
            <Button
              disabled={busy}
              onClick={() => void reconnect()}
              size="sm"
              type="button"
              variant="outline"
            >
              {busy ? <Loader2 className="animate-spin" /> : <RefreshCw />}
              Reconnect
            </Button>
            <Button
              disabled={busy}
              onClick={() => void editCredential()}
              size="sm"
              type="button"
              variant="outline"
            >
              Edit credentials
            </Button>
          </div>
        ) : null}
      </div>

      {details}
    </div>
  );
};
