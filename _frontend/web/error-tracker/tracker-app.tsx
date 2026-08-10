import { Archive, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { Button } from "@/components/ui/button";

import {
  ApiError,
  api,
  configureApi,
  setAdminToken,
  subscribeUnauthorized,
} from "./api";
import { AuthScreen } from "./auth-screen";
import { errorMessage } from "./format";
import { TrackerConsole } from "./tracker-console";
import type { ApiToken, App, Tracker } from "./types";

export interface TrackerSnapshot {
  apiTokens: ApiToken[];
  apps: App[];
  tracker: Tracker;
}

const LoadingScreen = () => (
  <main className="relative grid h-full place-items-center overflow-hidden bg-background">
    <div className="tracker-grid pointer-events-none absolute inset-0 opacity-40" />
    <div className="relative flex flex-col items-center gap-4 text-[9px] tracking-[0.14em] text-muted-foreground uppercase">
      <div className="grid size-14 place-items-center border border-border bg-card text-sm font-bold text-foreground">
        et
      </div>
      <span className="flex items-center gap-2">
        <RefreshCw className="size-3 animate-spin" /> Opening index
      </span>
    </div>
  </main>
);

const FatalScreen = ({
  error,
  retry,
}: {
  error: string;
  retry: () => void;
}) => (
  <main className="grid h-full place-items-center bg-background p-8 text-center">
    <div className="max-w-md">
      <div className="mx-auto grid size-14 place-items-center border border-destructive/40 text-destructive">
        <Archive className="size-5" />
      </div>
      <h1 className="mt-6 text-sm font-medium">Tracker unavailable</h1>
      <p className="mt-2 text-[10px] leading-5 text-muted-foreground">
        {error}
      </p>
      <Button className="mt-5" onClick={retry} variant="outline">
        <RefreshCw /> Retry
      </Button>
    </div>
  </main>
);

export const TrackerApp = ({
  adminAuthentication = true,
  apiBasePath = "",
  embedded = false,
}: {
  adminAuthentication?: boolean;
  apiBasePath?: string;
  embedded?: boolean;
}) => {
  configureApi({ adminAuthentication, basePath: apiBasePath });
  const [snapshot, setSnapshot] = useState<TrackerSnapshot>();
  const [locked, setLocked] = useState(false);
  const [loading, setLoading] = useState(true);
  const [fatalError, setFatalError] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setFatalError("");
    try {
      const [tracker, apps, apiTokens] = await Promise.all([
        api.tracker(),
        api.apps(),
        api.tokens(),
      ]);
      setSnapshot({ apiTokens, apps, tracker });
      setLocked(false);
    } catch (error) {
      if (
        adminAuthentication &&
        error instanceof ApiError &&
        error.status === 401
      ) {
        setLocked(true);
      } else {
        setFatalError(errorMessage(error, "Unable to open tracker"));
      }
      throw error;
    } finally {
      setLoading(false);
    }
  }, [adminAuthentication]);

  const loadSafely = useCallback(async () => {
    try {
      await load();
    } catch {
      // The load function already maps authentication and transport failures to UI state.
    }
  }, [load]);

  const handleUnauthorized = useCallback(() => {
    if (adminAuthentication) {
      setLocked(true);
    }
  }, [adminAuthentication]);

  useEffect(() => {
    const unsubscribe = subscribeUnauthorized(handleUnauthorized);
    const timeout = setTimeout(() => void loadSafely(), 0);
    return () => {
      clearTimeout(timeout);
      unsubscribe();
    };
  }, [handleUnauthorized, loadSafely]);

  const lock = () => {
    setAdminToken("");
    setLocked(true);
  };
  if (adminAuthentication && locked) {
    return <AuthScreen onSuccess={load} />;
  }
  if (loading && !snapshot) {
    return <LoadingScreen />;
  }
  if (fatalError && !snapshot) {
    return <FatalScreen error={fatalError} retry={() => void loadSafely()} />;
  }
  if (!snapshot) {
    return <LoadingScreen />;
  }
  return (
    <TrackerConsole
      embedded={embedded}
      onLock={lock}
      refreshSnapshot={load}
      snapshot={snapshot}
    />
  );
};
