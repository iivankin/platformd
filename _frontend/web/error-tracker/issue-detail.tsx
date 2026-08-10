import { Activity, Clock3 } from "lucide-react";
import { useState } from "react";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

import { api } from "./api";
import { Eyebrow, StatusBadge } from "./common-ui";
import { EventEvidence } from "./event-detail";
import { errorMessage, formatTime, shortId } from "./format";
import type { DetailTarget, EventDetail, Issue, IssueDetail } from "./types";

const Fact = ({ label, value }: { label: string; value: React.ReactNode }) => (
  <div className="min-w-32 border-l border-border pl-3 first:border-l-0 first:pl-0">
    <p className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
      {label}
    </p>
    <div className="mt-1.5 text-[10px] text-foreground/80">{value}</div>
  </div>
);

export const IssueDetailView = ({
  appId,
  detail,
  latestEvent,
  notify,
  onIssueUpdated,
  openDetail,
}: {
  appId: string;
  detail: IssueDetail;
  latestEvent?: EventDetail;
  notify: (message: string) => void;
  onIssueUpdated: () => void;
  openDetail: (target: DetailTarget) => void;
}) => {
  const [status, setStatus] = useState(detail.issue.status);
  const [pending, setPending] = useState(false);
  const updateStatus = async (value: unknown) => {
    const next = String(value) as Issue["status"];
    setStatus(next);
    setPending(true);
    try {
      await api.updateIssue(appId, detail.issue.id, next);
      notify("Issue state updated");
      onIssueUpdated();
    } catch (error) {
      setStatus(detail.issue.status);
      notify(errorMessage(error, "Unable to update issue"));
    } finally {
      setPending(false);
    }
  };
  return (
    <>
      <section className="border-b border-border px-5 py-6 lg:px-7">
        <div className="flex max-w-6xl items-start justify-between gap-8 max-md:flex-col">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <StatusBadge value={detail.issue.level} />
              <span className="text-[9px] text-muted-foreground">
                {detail.issue.platform || "other"}
              </span>
              <span className="text-[9px] text-muted-foreground">
                {shortId(detail.issue.id, 16)}
              </span>
            </div>
            <h1 className="mt-3 max-w-4xl text-xl leading-8 font-medium tracking-[-0.04em]">
              {detail.issue.title}
            </h1>
          </div>
          <div className="shrink-0">
            <label
              className="mb-1.5 block text-[8px] tracking-[0.1em] text-muted-foreground uppercase"
              htmlFor="issue-status"
            >
              Issue status
            </label>
            <Select
              disabled={pending}
              items={{ ignored: "Ignored", open: "Open", resolved: "Resolved" }}
              onValueChange={(value) => void updateStatus(value)}
              value={status}
            >
              <SelectTrigger className="w-36" id="issue-status">
                <SelectValue />
              </SelectTrigger>
              <SelectContent align="end">
                <SelectItem value="open">Open</SelectItem>
                <SelectItem value="resolved">Resolved</SelectItem>
                <SelectItem value="ignored">Ignored</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </div>
        <div className="mt-6 flex max-w-4xl flex-wrap gap-x-6 gap-y-4 border-t border-border pt-4">
          <Fact
            label="Events"
            value={
              <span className="flex items-center gap-1.5">
                <Activity className="size-3" />
                {detail.issue.eventCount.toLocaleString()}
              </span>
            }
          />
          <Fact label="First seen" value={formatTime(detail.issue.firstSeen)} />
          <Fact label="Last seen" value={formatTime(detail.issue.lastSeen)} />
          <Fact label="State" value={<StatusBadge value={status} />} />
        </div>
      </section>

      {latestEvent ? (
        <EventEvidence
          appId={appId}
          detail={latestEvent}
          label="Latest event"
          notify={notify}
        />
      ) : null}

      <section className="border-t border-border px-5 py-6 lg:px-7">
        <div className="mb-4 flex items-end justify-between gap-4">
          <div>
            <Eyebrow>All events</Eyebrow>
            <p className="mt-1.5 text-[10px] text-muted-foreground">
              Chronological occurrences grouped into this issue.
            </p>
          </div>
          <span className="text-[9px] text-muted-foreground">
            {detail.eventTotal.toLocaleString()} total
          </span>
        </div>
        <div className="divide-y divide-border border-y border-border">
          {detail.events.map((event) => (
            <button
              className="grid w-full grid-cols-[28px_minmax(0,1fr)_auto] items-center gap-3 py-3 text-left hover:bg-muted/35"
              key={event.event_id}
              onClick={() => {
                if (event.event_id) {
                  openDetail({ id: event.event_id, kind: "event" });
                }
              }}
              type="button"
            >
              <span className="grid size-7 place-items-center border border-border text-[9px] text-muted-foreground uppercase">
                {(event.level ?? "E").slice(0, 1)}
              </span>
              <span className="min-w-0">
                <span className="block overflow-hidden text-[10px] font-medium text-ellipsis whitespace-nowrap">
                  {event.title ?? "Event"}
                </span>
                <span className="mt-1 block text-[9px] text-muted-foreground">
                  {shortId(event.event_id, 20)}
                </span>
              </span>
              <span className="flex items-center gap-1.5 text-[9px] text-muted-foreground">
                <Clock3 className="size-3" /> {formatTime(event.timestamp)}
              </span>
            </button>
          ))}
        </div>
      </section>
    </>
  );
};
