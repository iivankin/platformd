import { ArrowLeft, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";

import { api } from "./api";
import { EventDetailView } from "./event-detail";
import { errorMessage } from "./format";
import { IssueDetailView } from "./issue-detail";
import { ReplayDetailView } from "./replay-detail";
import type {
  App,
  DetailTarget,
  EventDetail,
  IssueDetail,
  ReplayDetail,
  ReplayRecording,
} from "./types";

type DetailData =
  | { kind: "event"; value: EventDetail }
  | {
      kind: "issue";
      latestEvent?: EventDetail;
      value: IssueDetail;
    }
  | { kind: "replay"; recording: ReplayRecording; value: ReplayDetail };

export const DetailPage = ({
  app,
  notify,
  onBack,
  onIssueUpdated,
  openDetail,
  target,
}: {
  app: App;
  notify: (message: string) => void;
  onBack: () => void;
  onIssueUpdated: () => void;
  openDetail: (target: DetailTarget) => void;
  target: DetailTarget;
}) => {
  const requestKey = `${app.id}:${target.kind}:${target.id}`;
  const [result, setResult] = useState<{
    data?: DetailData;
    error?: string;
    key: string;
  }>();

  useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        let next: DetailData;
        if (target.kind === "issue") {
          const issue = await api.issue(app.id, target.id);
          const latestEventId = issue.events[0]?.event_id;
          let latestEvent: EventDetail | undefined;
          if (latestEventId) {
            try {
              latestEvent = await api.event(app.id, latestEventId);
            } catch {
              // The issue remains useful when its latest raw event was already expired.
            }
          }
          next = { kind: "issue", latestEvent, value: issue };
        } else if (target.kind === "event") {
          next = { kind: "event", value: await api.event(app.id, target.id) };
        } else {
          const [value, recording] = await Promise.all([
            api.replay(app.id, target.id),
            api.replayRecording(app.id, target.id),
          ]);
          next = {
            kind: "replay",
            recording,
            value,
          };
        }
        if (active) {
          setResult({ data: next, key: requestKey });
        }
      } catch (loadError) {
        if (active) {
          setResult({
            error: errorMessage(loadError, "Unable to load detail"),
            key: requestKey,
          });
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [app.id, requestKey, target.id, target.kind]);

  const current = result?.key === requestKey ? result : undefined;
  return (
    <div className="min-h-full">
      <header className="flex h-12 items-center gap-3 border-b border-border px-3 lg:px-5">
        <Button onClick={onBack} size="sm" variant="ghost">
          <ArrowLeft /> Back
        </Button>
        <span className="h-4 w-px bg-border" />
        <p className="min-w-0 overflow-hidden text-[9px] tracking-[0.1em] text-ellipsis whitespace-nowrap text-muted-foreground uppercase">
          {app.slug} / {target.kind} / {target.id}
        </p>
      </header>
      {current ? null : (
        <div className="grid min-h-80 place-items-center text-[9px] tracking-[0.14em] text-muted-foreground uppercase">
          <span className="flex items-center gap-2">
            <LoaderCircle className="size-3.5 animate-spin" /> Loading detail
          </span>
        </div>
      )}
      {current?.error ? (
        <div className="grid min-h-80 place-items-center p-8 text-center text-[10px] text-destructive">
          {current.error}
        </div>
      ) : null}
      {current?.data?.kind === "event" ? (
        <EventDetailView
          appId={app.id}
          detail={current.data.value}
          notify={notify}
        />
      ) : null}
      {current?.data?.kind === "issue" ? (
        <IssueDetailView
          appId={app.id}
          detail={current.data.value}
          latestEvent={current.data.latestEvent}
          notify={notify}
          onIssueUpdated={onIssueUpdated}
          openDetail={openDetail}
        />
      ) : null}
      {current?.data?.kind === "replay" ? (
        <ReplayDetailView
          appId={app.id}
          detail={current.data.value}
          notify={notify}
          recording={current.data.recording}
          replayId={target.id}
        />
      ) : null}
    </div>
  );
};
