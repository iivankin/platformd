import { RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

import { api } from "./api";
import { ErrorView, LoadingView } from "./common-ui";
import { IssuesView } from "./data-views";
import { DetailPage } from "./detail-page";
import { errorMessage } from "./format";
import type { App, DetailTarget, Issue, ListResponse } from "./types";

const Toast = ({ message }: { message: string }) =>
  message ? (
    <output
      aria-live="polite"
      className="fixed right-4 bottom-4 z-[70] max-w-sm border border-border bg-foreground px-3 py-2 text-[10px] text-background shadow-xl"
    >
      {message}
    </output>
  ) : null;

export const ErrorsConsole = ({
  app,
  initialDetail,
  onInitialDetailClosed,
  onOpenTrace,
}: {
  app: App;
  initialDetail?: DetailTarget;
  onInitialDetailClosed?: () => void;
  onOpenTrace?: (traceId: string) => void;
}) => {
  const [query, setQuery] = useState("");
  const [issues, setIssues] = useState<{
    appId: string;
    payload: ListResponse<Issue>;
  }>();
  const [viewError, setViewError] = useState("");
  const [viewLoading, setViewLoading] = useState(false);
  const [revision, setRevision] = useState(0);
  const [detailStack, setDetailStack] = useState<DetailTarget[]>(() =>
    initialDetail ? [initialDetail] : []
  );
  const [toast, setToast] = useState("");
  const contentRef = useRef<HTMLElement>(null);
  const detail = detailStack.at(-1);
  const currentIssues = issues?.appId === app.id ? issues.payload : undefined;

  useEffect(() => {
    contentRef.current?.scrollTo({ top: 0 });
  }, [detail]);

  useEffect(() => {
    if (!toast) {
      return;
    }
    const timeout = setTimeout(() => setToast(""), 2600);
    return () => clearTimeout(timeout);
  }, [toast]);

  useEffect(() => {
    let active = true;
    const timeout = setTimeout(
      () => {
        const load = async () => {
          setViewLoading(true);
          setViewError("");
          try {
            const next = await api.issues(app.id, query);
            if (active) {
              setIssues({ appId: app.id, payload: next });
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
  }, [app.id, query, revision]);

  const notify = useCallback((message: string) => setToast(message), []);
  const refreshView = useCallback(() => setRevision((value) => value + 1), []);
  const openDetail = useCallback((target: DetailTarget) => {
    setDetailStack((current) => [...current, target]);
  }, []);
  const closeDetail = useCallback(() => {
    setDetailStack((current) => {
      if (current.length <= 1 && initialDetail) {
        onInitialDetailClosed?.();
      }
      return current.slice(0, -1);
    });
  }, [initialDetail, onInitialDetailClosed]);
  return (
    <div className="h-full min-h-0 bg-background">
      <main
        className="h-full min-h-0 min-w-0 overflow-y-auto [overflow-anchor:none]"
        ref={contentRef}
      >
        {detail ? (
          <DetailPage
            app={app}
            notify={notify}
            onBack={closeDetail}
            onIssueUpdated={refreshView}
            onOpenTrace={onOpenTrace}
            openDetail={openDetail}
            target={detail}
          />
        ) : (
          <>
            <div className="flex h-12 items-center justify-between gap-3 border-b border-border px-4">
              <div className="flex min-w-0 flex-1 items-center gap-2">
                <div className="relative w-full max-w-sm">
                  <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    aria-label="Search errors"
                    className="h-7 pl-7 text-[10px]"
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="Search errors"
                    type="search"
                    value={query}
                  />
                </div>
                {currentIssues ? (
                  <span className="shrink-0 text-[8px] text-muted-foreground">
                    {currentIssues.total.toLocaleString()} issues
                  </span>
                ) : null}
              </div>
              <Button
                aria-label="Refresh view"
                disabled={viewLoading}
                onClick={refreshView}
                size="icon"
                variant="ghost"
              >
                <RefreshCw className={cn(viewLoading && "animate-spin")} />
              </Button>
            </div>
            <section aria-live="polite">
              {viewLoading && !currentIssues ? <LoadingView /> : null}
              {viewError ? <ErrorView message={viewError} /> : null}
              {currentIssues && !viewError ? (
                <IssuesView
                  items={currentIssues.data}
                  openDetail={openDetail}
                />
              ) : null}
            </section>
          </>
        )}
      </main>
      <Toast message={toast} />
    </div>
  );
};
