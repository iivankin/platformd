import { LoaderCircle, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import {
  fetchManagedPostgresExtensions,
  fetchOperation,
  setManagedPostgresExtension,
} from "@/api";
import type { Operation, PostgresExtension } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const extensionActionLabel = (busy: boolean, installed: boolean): string => {
  if (!busy) {
    return installed ? "Uninstall" : "Install";
  }
  return installed ? "Removing…" : "Installing…";
};

const progressLabel = (progress?: string): string => {
  switch (progress) {
    case "queued": {
      return "Waiting to start";
    }
    case "resolving_base_image": {
      return "Checking the PostgreSQL image";
    }
    case "downloading_source": {
      return "Downloading verified pgvector source";
    }
    case "building_image": {
      return "Building the local PostgreSQL image";
    }
    case "committing_image": {
      return "Saving the local image";
    }
    case "using_cached_image": {
      return "Using the cached local image";
    }
    case "updating_configuration": {
      return "Saving extension configuration";
    }
    case "restarting_database": {
      return "Restarting PostgreSQL";
    }
    case "creating_extension": {
      return "Creating the extension";
    }
    case "dropping_extension": {
      return "Removing the extension from the database";
    }
    default: {
      return "Applying extension change";
    }
  }
};

const operationPollMilliseconds = 750;

const extensionDisplayName = (name: string): string =>
  name === "vector" ? "pgvector" : name;

const ExtensionRows = ({
  busy,
  extensions,
  installed,
  onChange,
}: {
  busy?: string;
  extensions: PostgresExtension[];
  installed: boolean;
  onChange: (extension: PostgresExtension, installed: boolean) => void;
}) => (
  <>
    <header className="flex items-center justify-between border-b border-border bg-muted/15 px-5 py-2.5">
      <h4 className="text-[9px] font-medium tracking-[0.08em] uppercase">
        {installed ? "Installed" : "Available"}
      </h4>
      <span className="text-[9px] text-muted-foreground">
        {extensions.length.toLocaleString()}
      </span>
    </header>
    {extensions.map((extension) => (
      <div
        className="grid min-h-14 grid-cols-[minmax(10rem,14rem)_7rem_minmax(0,1fr)_6rem] items-center border-b border-border px-5 text-[10px]"
        key={extension.name}
      >
        <code className="truncate pr-4">
          {extensionDisplayName(extension.name)}
        </code>
        <span className="text-muted-foreground">
          {extension.installedVersion ?? extension.defaultVersion}
        </span>
        <span className="truncate pr-4 text-muted-foreground">
          {extension.comment}
        </span>
        <Button
          disabled={Boolean(busy)}
          onClick={() => onChange(extension, !installed)}
          size="sm"
          variant="outline"
        >
          {extensionActionLabel(busy === extension.name, installed)}
        </Button>
      </div>
    ))}
    {extensions.length === 0 ? (
      <p className="border-b border-border px-5 py-6 text-center text-[10px] text-muted-foreground">
        {installed
          ? "No installed extensions match this search."
          : "No available extensions match this search."}
      </p>
    ) : null}
  </>
);

export const PostgresExtensions = ({
  postgresID,
  projectID,
}: {
  postgresID: string;
  projectID: string;
}) => {
  const [extensions, setExtensions] = useState<PostgresExtension[]>([]);
  const [search, setSearch] = useState("");
  const [busy, setBusy] = useState<string>();
  const [operation, setOperation] = useState<Operation>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();

  const load = useCallback(
    async (signal?: AbortSignal) => {
      try {
        setExtensions(
          await fetchManagedPostgresExtensions(projectID, postgresID, signal)
        );
        setError(undefined);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load PostgreSQL extensions"
        );
      } finally {
        setLoading(false);
      }
    },
    [postgresID, projectID]
  );

  useEffect(() => {
    const controller = new AbortController();
    const loadInitialExtensions = async () => {
      try {
        const initial = await fetchManagedPostgresExtensions(
          projectID,
          postgresID,
          controller.signal
        );
        setExtensions(initial);
        setError(undefined);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load PostgreSQL extensions"
        );
      } finally {
        setLoading(false);
      }
    };
    void loadInitialExtensions();
    return () => controller.abort();
  }, [postgresID, projectID]);

  const visible = useMemo(() => {
    const needle = search.trim().toLowerCase();
    if (!needle) {
      return extensions;
    }
    return extensions.filter(
      (extension) =>
        extension.name.toLowerCase().includes(needle) ||
        extensionDisplayName(extension.name).toLowerCase().includes(needle) ||
        extension.comment.toLowerCase().includes(needle)
    );
  }, [extensions, search]);
  const installed = visible.filter((extension) => extension.installedVersion);
  const available = visible.filter((extension) => !extension.installedVersion);

  const change = async (
    extension: PostgresExtension,
    shouldInstall: boolean
  ) => {
    setBusy(extension.name);
    setError(undefined);
    try {
      const started = await setManagedPostgresExtension(
        projectID,
        postgresID,
        extension.name,
        shouldInstall
      );
      setOperation(started);
      if (started.status === "succeeded") {
        setLoading(true);
        await load();
        setBusy(undefined);
      } else if (started.status !== "running") {
        setError(started.errorMessage ?? "PostgreSQL extension change failed");
        setBusy(undefined);
      }
    } catch (changeError) {
      setError(
        changeError instanceof Error
          ? changeError.message
          : "Unable to change PostgreSQL extension"
      );
      setBusy(undefined);
    }
  };

  useEffect(() => {
    if (!operation || operation.status !== "running") {
      return;
    }
    const controller = new AbortController();
    let inFlight = false;
    const poll = async () => {
      if (inFlight) {
        return;
      }
      inFlight = true;
      try {
        const current = await fetchOperation(operation.id, controller.signal);
        if (current.status === "succeeded") {
          setLoading(true);
          await load(controller.signal);
          setBusy(undefined);
        } else if (current.status !== "running") {
          setError(
            current.errorMessage ?? "PostgreSQL extension change failed"
          );
          setBusy(undefined);
        }
        setOperation(current);
      } catch (pollError) {
        if (
          !(
            pollError instanceof DOMException && pollError.name === "AbortError"
          )
        ) {
          setError(
            pollError instanceof Error
              ? pollError.message
              : "Unable to read PostgreSQL extension progress"
          );
        }
      } finally {
        inFlight = false;
      }
    };
    const interval = window.setInterval(
      () => void poll(),
      operationPollMilliseconds
    );
    return () => {
      controller.abort();
      window.clearInterval(interval);
    };
  }, [load, operation]);

  return (
    <section>
      <header className="flex items-center justify-between gap-4 border-b border-border px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">Extensions</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Image extensions are built once, cached locally, and restored from
            this configuration when the cache is missing.
          </p>
        </div>
        <Button
          aria-label="Refresh PostgreSQL extensions"
          disabled={loading}
          onClick={() => {
            setLoading(true);
            void load();
          }}
          size="icon"
          variant="ghost"
        >
          <RefreshCw />
        </Button>
      </header>
      <div className="border-b border-border px-5 py-3">
        <div className="relative max-w-xl">
          <Search className="pointer-events-none absolute top-1/2 left-3 size-3 -translate-y-1/2 text-muted-foreground" />
          <Input
            aria-label="Search PostgreSQL extensions"
            className="pl-8"
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Search extensions"
            value={search}
          />
        </div>
      </div>
      {busy ? (
        <div className="flex items-center gap-2 border-b border-border bg-muted/15 px-5 py-2.5 text-[10px]">
          <LoaderCircle className="size-3 animate-spin text-muted-foreground" />
          <span>{progressLabel(operation?.progress)}</span>
          <code className="ml-auto text-muted-foreground">{busy}</code>
        </div>
      ) : null}
      {error ? (
        <p className="border-b border-destructive/30 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      {loading && extensions.length === 0 ? (
        <p className="border-b border-border px-5 py-8 text-center text-[10px] text-muted-foreground">
          Loading extensions…
        </p>
      ) : (
        <>
          <ExtensionRows
            busy={busy}
            extensions={installed}
            installed
            onChange={(extension, shouldInstall) =>
              void change(extension, shouldInstall)
            }
          />
          <ExtensionRows
            busy={busy}
            extensions={available}
            installed={false}
            onChange={(extension, shouldInstall) =>
              void change(extension, shouldInstall)
            }
          />
        </>
      )}
    </section>
  );
};
