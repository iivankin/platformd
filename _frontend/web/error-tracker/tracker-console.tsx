import {
  Activity,
  Bug,
  LockKeyhole,
  Moon,
  PackageSearch,
  Play,
  PlugZap,
  RefreshCw,
  Search,
  Settings,
  Sun,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { useTheme } from "@/use-theme";

import { api } from "./api";
import { ApplicationView } from "./application-view";
import type { ViewData } from "./application-view";
import { CreateAppDialog } from "./create-app-dialog";
import { DetailPage } from "./detail-page";
import { errorMessage } from "./format";
import type { TrackerSnapshot } from "./tracker-app";
import { TrackerSettingsView } from "./tracker-settings-view";
import type { CreatedApp, DetailTarget, ViewName } from "./types";

const views: { icon: typeof Bug; label: string; value: ViewName }[] = [
  { icon: Bug, label: "Issues", value: "issues" },
  { icon: Activity, label: "Events", value: "events" },
  { icon: Play, label: "Replays", value: "replays" },
  { icon: PackageSearch, label: "Artifacts", value: "artifacts" },
  { icon: Settings, label: "Settings", value: "settings" },
];

const Toast = ({ message }: { message: string }) => {
  if (!message) {
    return null;
  }
  return (
    <output
      aria-live="polite"
      className="fixed right-4 bottom-4 z-[70] max-w-sm border border-border bg-foreground px-3 py-2 text-[10px] text-background shadow-xl"
    >
      {message}
    </output>
  );
};

const ThemeButton = ({
  dark,
  onToggle,
}: {
  dark: boolean;
  onToggle: () => void;
}) => (
  <Button
    aria-label={`Use ${dark ? "light" : "dark"} theme`}
    onClick={onToggle}
    size="icon"
    variant="ghost"
  >
    {dark ? <Sun /> : <Moon />}
  </Button>
);

const SessionLockButton = ({
  onLock,
  required,
}: {
  onLock: () => void;
  required: boolean;
}) => {
  if (!required) {
    return null;
  }
  return (
    <Button
      aria-label="Lock admin session"
      onClick={onLock}
      size="icon"
      variant="ghost"
    >
      <LockKeyhole />
    </Button>
  );
};

const TrackerHeader = ({
  dark,
  hidden,
  onLock,
  onThemeToggle,
  snapshot,
}: {
  dark: boolean;
  hidden: boolean;
  onLock: () => void;
  onThemeToggle: () => void;
  snapshot: TrackerSnapshot;
}) => {
  if (hidden) {
    return null;
  }
  return (
    <header className="col-span-2 flex items-center justify-between border-b border-border bg-background px-3">
      <div className="flex min-w-0 items-center gap-3">
        <div className="grid size-7 shrink-0 place-items-center border border-border text-[10px] font-bold">
          et
        </div>
        <div className="min-w-0">
          <p className="overflow-hidden text-[11px] font-medium text-ellipsis whitespace-nowrap">
            {snapshot.tracker.name}
          </p>
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Error tracker
          </p>
        </div>
      </div>
      <div className="flex items-center gap-1">
        <span className="mr-2 flex items-center gap-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase max-md:hidden">
          <i className="size-1.5 bg-emerald-500 shadow-[0_0_10px_oklch(0.7_0.16_145/0.6)]" />{" "}
          Index online
        </span>
        <ThemeButton dark={dark} onToggle={onThemeToggle} />
        <SessionLockButton
          onLock={onLock}
          required={snapshot.tracker.adminAuthRequired}
        />
      </div>
    </header>
  );
};

const trackerLayoutClassName = (embedded: boolean) =>
  embedded ? "grid-rows-[minmax(0,1fr)]" : "grid-rows-[48px_minmax(0,1fr)]";

export const TrackerConsole = ({
  embedded = false,
  refreshSnapshot,
  snapshot,
  onLock,
}: {
  embedded?: boolean;
  onLock: () => void;
  refreshSnapshot: () => Promise<void>;
  snapshot: TrackerSnapshot;
}) => {
  const [selectedAppId, setSelectedAppId] = useState(
    snapshot.apps[0]?.id ?? ""
  );
  const [trackerSettingsOpen, setTrackerSettingsOpen] = useState(false);
  const [view, setView] = useState<ViewName>("issues");
  const [query, setQuery] = useState("");
  const [viewData, setViewData] = useState<ViewData>();
  const [viewError, setViewError] = useState("");
  const [viewLoading, setViewLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  const [detailStack, setDetailStack] = useState<DetailTarget[]>([]);
  const [toast, setToast] = useState("");
  const contentRef = useRef<HTMLElement>(null);
  const { setTheme } = useTheme();
  const darkTheme = document.documentElement.classList.contains("dark");
  const selectedApp = useMemo(
    () =>
      snapshot.apps.find((app) => app.id === selectedAppId) ?? snapshot.apps[0],
    [selectedAppId, snapshot.apps]
  );
  const detail = detailStack.at(-1);

  useEffect(() => {
    contentRef.current?.scrollTo({ top: 0 });
  }, [detail, selectedAppId, trackerSettingsOpen, view]);

  useEffect(() => {
    if (!toast) {
      return;
    }
    const timeout = setTimeout(() => setToast(""), 2600);
    return () => clearTimeout(timeout);
  }, [toast]);

  useEffect(() => {
    if (!selectedApp || trackerSettingsOpen || view === "settings") {
      return;
    }
    let active = true;
    const timeout = setTimeout(
      () => {
        const load = async () => {
          setViewLoading(true);
          setViewError("");
          try {
            let next: ViewData;
            if (view === "issues") {
              next = {
                kind: view,
                payload: await api.issues(selectedApp.id, query),
              };
            } else if (view === "events") {
              next = {
                kind: view,
                payload: await api.events(selectedApp.id, query),
              };
            } else if (view === "replays") {
              next = { kind: view, payload: await api.replays(selectedApp.id) };
            } else {
              next = {
                kind: view,
                payload: await api.artifacts(selectedApp.id, query),
              };
            }
            if (active) {
              setViewData(next);
            }
          } catch (error) {
            if (active) {
              setViewError(errorMessage(error, "Unable to query the index"));
            }
          } finally {
            if (active) {
              setViewLoading(false);
            }
          }
        };
        void load();
      },
      query ? 220 : 0
    );
    return () => {
      active = false;
      clearTimeout(timeout);
    };
  }, [query, revision, selectedApp, trackerSettingsOpen, view]);

  const notify = useCallback((message: string) => setToast(message), []);
  const refreshView = useCallback(() => {
    setRevision((value) => value + 1);
  }, []);
  const handleCreatedApp = async (app: CreatedApp) => {
    setSelectedAppId(app.id);
    setTrackerSettingsOpen(false);
    await refreshSnapshot();
    notify("Application created");
  };
  const selectApp = (appId: string) => {
    setSelectedAppId(appId);
    setTrackerSettingsOpen(false);
    setQuery("");
    setDetailStack([]);
  };
  const selectView = (next: ViewName) => {
    setView(next);
    setTrackerSettingsOpen(false);
    setQuery("");
    setDetailStack([]);
  };
  const selectTrackerSettings = () => {
    setTrackerSettingsOpen(true);
    setQuery("");
    setDetailStack([]);
  };
  const openDetail = useCallback((target: DetailTarget) => {
    setDetailStack((current) => [...current, target]);
  }, []);
  const closeDetail = useCallback(() => {
    setDetailStack((current) => current.slice(0, -1));
  }, []);
  const copyDsn = async () => {
    if (!selectedApp) {
      return;
    }
    try {
      await navigator.clipboard.writeText(selectedApp.dsn);
      notify("DSN copied to clipboard");
    } catch (error) {
      notify(errorMessage(error, "Clipboard access failed"));
    }
  };
  const toggleTheme = () => {
    const dark = document.documentElement.classList.contains("dark");
    setTheme(dark ? "light" : "dark");
  };

  return (
    <div
      className={cn(
        "grid h-full min-h-0 grid-cols-[220px_minmax(0,1fr)] max-sm:grid-cols-[72px_minmax(0,1fr)]",
        trackerLayoutClassName(embedded)
      )}
    >
      <TrackerHeader
        dark={darkTheme}
        hidden={embedded}
        onLock={onLock}
        onThemeToggle={toggleTheme}
        snapshot={snapshot}
      />

      <aside className="flex min-h-0 flex-col border-r border-border bg-sidebar text-sidebar-foreground">
        <div className="flex h-10 shrink-0 items-center justify-between border-b border-sidebar-border px-3 max-sm:justify-center">
          <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase max-sm:hidden">
            Applications
          </span>
          <CreateAppDialog compact onCreated={handleCreatedApp} />
        </div>
        <nav
          className="min-h-0 flex-1 overflow-y-auto py-1"
          aria-label="Applications"
        >
          {snapshot.apps.map((app) => {
            const active = !trackerSettingsOpen && selectedApp?.id === app.id;
            return (
              <button
                className={cn(
                  "grid w-full grid-cols-[28px_minmax(0,1fr)] items-center gap-2 border-l-2 border-transparent px-3 py-2.5 text-left hover:bg-sidebar-accent max-sm:grid-cols-1 max-sm:justify-items-center max-sm:px-2",
                  active && "border-sidebar-primary bg-sidebar-accent"
                )}
                key={app.id}
                onClick={() => selectApp(app.id)}
                type="button"
              >
                <span className="grid size-7 place-items-center border border-sidebar-border text-[9px] text-muted-foreground uppercase">
                  {app.name.slice(0, 2)}
                </span>
                <span className="min-w-0 max-sm:hidden">
                  <span className="block overflow-hidden text-[10px] font-medium text-ellipsis whitespace-nowrap">
                    {app.name}
                  </span>
                  <span className="mt-1 block text-[8px] text-muted-foreground">
                    Project {app.projectId}
                  </span>
                </span>
              </button>
            );
          })}
        </nav>
        <button
          aria-current={trackerSettingsOpen ? "page" : undefined}
          className={cn(
            "flex h-10 shrink-0 items-center gap-2 border-t border-l-2 border-sidebar-border border-l-transparent px-3 text-left text-[8px] text-muted-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground max-sm:justify-center",
            trackerSettingsOpen &&
              "border-l-sidebar-primary bg-sidebar-accent text-sidebar-accent-foreground"
          )}
          onClick={selectTrackerSettings}
          type="button"
        >
          <PlugZap className="size-3" />
          <span className="max-sm:hidden">MCP &amp; API</span>
        </button>
      </aside>

      <main
        className="min-h-0 min-w-0 overflow-y-auto bg-background [overflow-anchor:none]"
        ref={contentRef}
      >
        {trackerSettingsOpen ? (
          <>
            <section className="flex min-h-28 items-end justify-between gap-8 border-b border-border bg-gradient-to-br from-muted/45 to-background px-5 py-5 lg:px-7">
              <div className="min-w-0">
                <p className="text-[9px] tracking-[0.14em] text-muted-foreground uppercase">
                  {snapshot.tracker.slug} / Control plane
                </p>
                <h1 className="mt-2 text-2xl font-medium tracking-[-0.04em]">
                  MCP &amp; API
                </h1>
              </div>
              <span className="text-[9px] text-muted-foreground tabular-nums max-sm:hidden">
                {snapshot.apps.length} application
                {snapshot.apps.length === 1 ? "" : "s"}
              </span>
            </section>
            <TrackerSettingsView
              apiTokens={snapshot.apiTokens}
              apps={snapshot.apps}
              notify={notify}
              refresh={refreshSnapshot}
              tracker={snapshot.tracker}
            />
          </>
        ) : null}
        {!trackerSettingsOpen && selectedApp && detail ? (
          <DetailPage
            app={selectedApp}
            notify={notify}
            onBack={closeDetail}
            onIssueUpdated={refreshView}
            openDetail={openDetail}
            target={detail}
          />
        ) : null}
        {!trackerSettingsOpen && selectedApp && !detail ? (
          <>
            <section className="flex min-h-28 items-end justify-between gap-8 border-b border-border bg-gradient-to-br from-muted/45 to-background px-5 py-5 lg:px-7">
              <div className="min-w-0">
                <p className="text-[9px] tracking-[0.14em] text-muted-foreground uppercase">
                  {selectedApp.slug} / Project {selectedApp.projectId}
                </p>
                <h1 className="mt-2 overflow-hidden text-2xl font-medium tracking-[-0.04em] text-ellipsis whitespace-nowrap">
                  {selectedApp.name}
                </h1>
              </div>
              <div className="max-w-[54%] min-w-0 max-md:hidden">
                <p className="mb-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                  Client DSN
                </p>
                <div className="flex items-center gap-2">
                  <code className="min-w-0 overflow-hidden text-[9px] text-ellipsis whitespace-nowrap text-foreground/70">
                    {selectedApp.dsn}
                  </code>
                  <Button
                    onClick={() => void copyDsn()}
                    size="sm"
                    variant="outline"
                  >
                    Copy
                  </Button>
                </div>
              </div>
            </section>

            <nav
              className="flex h-11 items-stretch overflow-x-auto border-b border-border px-3"
              aria-label="Application views"
            >
              {views.map(({ icon: Icon, label, value }) => (
                <button
                  className={cn(
                    "relative flex shrink-0 items-center gap-1.5 px-3 text-[9px] tracking-[0.08em] text-muted-foreground uppercase after:absolute after:right-3 after:bottom-0 after:left-3 after:h-px after:bg-transparent hover:text-foreground",
                    view === value && "text-foreground after:bg-foreground"
                  )}
                  key={value}
                  onClick={() => selectView(value)}
                  type="button"
                >
                  <Icon className="size-3" /> {label}
                </button>
              ))}
            </nav>

            {view === "settings" ? null : (
              <div className="flex h-12 items-center justify-between gap-3 border-b border-border px-4">
                <div className="flex min-w-0 flex-1 items-center gap-2">
                  {view === "replays" ? null : (
                    <div className="relative w-full max-w-sm">
                      <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3 -translate-y-1/2 text-muted-foreground" />
                      <Input
                        aria-label="Search current view"
                        className="h-7 pl-7 text-[10px]"
                        onChange={(event) => setQuery(event.target.value)}
                        placeholder={`Search ${view}`}
                        type="search"
                        value={query}
                      />
                    </div>
                  )}
                  {viewData ? (
                    <span className="shrink-0 text-[8px] text-muted-foreground">
                      {viewData.payload.total.toLocaleString()} records
                    </span>
                  ) : null}
                </div>
                <Button
                  aria-label="Refresh view"
                  disabled={viewLoading}
                  onClick={() => void refreshView()}
                  size="icon"
                  variant="ghost"
                >
                  <RefreshCw className={cn(viewLoading && "animate-spin")} />
                </Button>
              </div>
            )}
            <section aria-live="polite">
              <ApplicationView
                app={selectedApp}
                error={viewError}
                loading={viewLoading}
                notify={notify}
                openDetail={openDetail}
                refresh={refreshSnapshot}
                tracker={snapshot.tracker}
                view={view}
                viewData={viewData}
              />
            </section>
          </>
        ) : null}
        {trackerSettingsOpen || selectedApp ? null : (
          <section className="relative grid min-h-full place-items-center overflow-hidden px-8 py-16">
            <div className="tracker-grid pointer-events-none absolute inset-0 opacity-55" />
            <div className="relative max-w-xl text-center">
              <p className="text-[9px] tracking-[0.18em] text-muted-foreground uppercase">
                No applications configured
              </p>
              <h1 className="mt-4 text-4xl font-medium tracking-[-0.06em] sm:text-5xl">
                Errors become evidence.
              </h1>
              <p className="mx-auto mt-4 max-w-lg text-[11px] leading-6 text-muted-foreground">
                Create an application to receive Sentry envelopes, retain
                replays, and resolve native or source-mapped stacks.
              </p>
              <div className="mt-6 flex justify-center">
                <CreateAppDialog onCreated={handleCreatedApp} />
              </div>
            </div>
          </section>
        )}
      </main>
      <Toast message={toast} />
    </div>
  );
};
