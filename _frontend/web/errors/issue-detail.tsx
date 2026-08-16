import { Activity, ArrowLeft, ArrowRight, Clock3, Users } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

import { api } from "./api";
import { Eyebrow, StatusBadge } from "./common-ui";
import { asRecord } from "./event-context";
import { EventEvidence } from "./event-detail";
import { errorMessage, formatTime, shortId } from "./format";
import type {
  DetailTarget,
  EventDetail,
  Issue,
  IssueDetail,
  StoredDocument,
} from "./types";

const Fact = ({ label, value }: { label: string; value: React.ReactNode }) => (
  <div className="min-w-32 border-l border-border pl-3 first:border-l-0 first:pl-0">
    <p className="text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
      {label}
    </p>
    <div className="mt-1.5 text-[10px] text-foreground/80">{value}</div>
  </div>
);

const IssueActivity = ({ detail }: { detail: IssueDetail }) => {
  if (detail.activity.length === 0) {
    return null;
  }
  let first = Date.parse(detail.issue.firstSeen);
  let last = Date.parse(detail.issue.lastSeen);
  if (!Number.isFinite(first)) {
    first = Number.isFinite(last) ? last : 0;
  }
  if (!Number.isFinite(last)) {
    last = first;
  }
  const bins = Array.from({ length: 32 }, () => 0);
  for (const value of detail.activity) {
    if (value.bin >= 0 && value.bin < bins.length) {
      bins[value.bin] = value.count;
    }
  }
  const peak = Math.max(...bins, 1);
  return (
    <div className="min-w-0 flex-1 py-4 pr-5 pl-5 lg:pl-7">
      <div className="mb-2 flex items-center justify-between text-[8px] text-muted-foreground">
        <span>Occurrences</span>
        <span>{detail.eventTotal.toLocaleString()} total</span>
      </div>
      <div
        aria-label="Issue occurrence timeline"
        className="flex h-16 items-end gap-px border-b border-border"
      >
        {bins.map((count, index) => (
          <span
            className={cn(
              "min-w-0 flex-1 bg-muted-foreground/20",
              count > 0 && "bg-destructive/65"
            )}
            key={`${index}:${count}`}
            style={{
              height:
                count > 0 ? `${Math.max(10, (count / peak) * 100)}%` : "2px",
            }}
            title={`${count} event${count === 1 ? "" : "s"}`}
          />
        ))}
      </div>
      <div className="mt-1 flex justify-between text-[8px] text-muted-foreground tabular-nums">
        <span>{formatTime(new Date(first).toISOString())}</span>
        <span>{formatTime(new Date(last).toISOString())}</span>
      </div>
    </div>
  );
};

const IssueDistributions = ({ detail }: { detail: IssueDetail }) => {
  const preferred = [
    "release",
    "environment",
    "browser",
    "device",
    "os",
    "platform",
    "user",
  ];
  const rows = preferred.flatMap((key) => {
    const value = detail.distributions.find((entry) => entry.key === key);
    return value ? [value] : [];
  });
  if (rows.length === 0) {
    return null;
  }
  return (
    <div className="w-full border-t border-border px-5 py-4 lg:w-[27rem] lg:border-t-0 lg:border-l lg:px-6">
      <p className="mb-3 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
        Top affected
      </p>
      <div className="space-y-2.5">
        {rows.map((row) => {
          const ratio = Math.min(
            100,
            Math.max(2, (row.count / Math.max(1, detail.eventTotal)) * 100)
          );
          return (
            <div
              className="grid grid-cols-[5rem_minmax(0,1fr)_auto] items-center gap-2 text-[9px]"
              key={row.key}
            >
              <span className="truncate text-muted-foreground">{row.key}</span>
              <span className="relative h-1.5 overflow-hidden bg-muted">
                <span
                  className="absolute inset-y-0 left-0 bg-destructive/65"
                  style={{ width: `${ratio}%` }}
                />
              </span>
              <span className="max-w-36 truncate" title={row.value}>
                {row.value}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
};

const eventTraceId = (event?: StoredDocument) => {
  const trace = asRecord(asRecord(asRecord(event?.payload)?.contexts)?.trace);
  return typeof trace?.trace_id === "string" ? trace.trace_id : undefined;
};

const RelatedIssues = ({
  appId,
  event,
  issue,
  openDetail,
}: {
  appId: string;
  event?: StoredDocument;
  issue: Issue;
  openDetail: (target: DetailTarget) => void;
}) => {
  const [related, setRelated] = useState<
    { id: string; relation: string; title: string }[]
  >([]);
  const traceId = eventTraceId(event);
  useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        const [traceEvents, similar] = await Promise.all([
          traceId
            ? api.events(appId, traceId)
            : Promise.resolve({ data: [], total: 0 }),
          api.issues(appId, issue.title),
        ]);
        const next = new Map<
          string,
          { id: string; relation: string; title: string }
        >();
        for (const candidate of traceEvents.data) {
          if (candidate.issue_id && candidate.issue_id !== issue.id) {
            next.set(candidate.issue_id, {
              id: candidate.issue_id,
              relation: "Same trace",
              title: candidate.title ?? "Related issue",
            });
          }
        }
        for (const candidate of similar.data) {
          if (candidate.id !== issue.id && !next.has(candidate.id)) {
            next.set(candidate.id, {
              id: candidate.id,
              relation: "Similar error",
              title: candidate.title,
            });
          }
        }
        if (active) {
          setRelated([...next.values()].slice(0, 5));
        }
      } catch {
        if (active) {
          setRelated([]);
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [appId, issue.id, issue.title, traceId]);
  if (related.length === 0) {
    return null;
  }
  return (
    <section className="border-t border-border px-5 py-6 lg:px-7">
      <Eyebrow>Related issues</Eyebrow>
      <div className="mt-3 divide-y divide-border border-y border-border">
        {related.map((candidate) => (
          <button
            className="grid w-full grid-cols-[7rem_minmax(0,1fr)_auto] items-center gap-3 py-3 text-left hover:bg-muted/25"
            key={candidate.id}
            onClick={() => openDetail({ id: candidate.id, kind: "issue" })}
            type="button"
          >
            <span className="text-[8px] tracking-[0.08em] text-muted-foreground uppercase">
              {candidate.relation}
            </span>
            <span className="truncate text-[10px]">{candidate.title}</span>
            <span className="font-mono text-[8px] text-muted-foreground">
              {shortId(candidate.id, 12)}
            </span>
          </button>
        ))}
      </div>
    </section>
  );
};

const currentEvent = (
  selectedID: string | undefined,
  latest: EventDetail | undefined,
  loaded: { id: string; value: EventDetail } | undefined
) => {
  if (latest?.event.event_id === selectedID) {
    return latest;
  }
  if (loaded && loaded.id === selectedID) {
    return loaded.value;
  }
};

export const IssueDetailView = ({
  appId,
  detail,
  latestEvent,
  notify,
  onIssueUpdated,
  onOpenTrace,
  openDetail,
}: {
  appId: string;
  detail: IssueDetail;
  latestEvent?: EventDetail;
  notify: (message: string) => void;
  onIssueUpdated: () => void;
  onOpenTrace?: (traceId: string) => void;
  openDetail: (target: DetailTarget) => void;
}) => {
  const [status, setStatus] = useState(detail.issue.status);
  const [pending, setPending] = useState(false);
  const [events, setEvents] = useState(detail.events);
  const [loadingMore, setLoadingMore] = useState(false);
  const firstId = detail.firstEventId || detail.events.at(-1)?.event_id;
  const latestId = detail.latestEventId || detail.events[0]?.event_id;
  const recommendedId = detail.recommendedEventId || latestId;
  const [selectedEventId, setSelectedEventId] = useState(
    recommendedId ?? latestId
  );
  const [loadedEvent, setLoadedEvent] = useState<
    { id: string; value: EventDetail } | undefined
  >();
  const selectedEvent = currentEvent(selectedEventId, latestEvent, loadedEvent);
  const selectedIndex = events.findIndex(
    (event) => event.event_id === selectedEventId
  );

  useEffect(() => {
    if (!selectedEventId) {
      return;
    }
    if (latestEvent?.event.event_id === selectedEventId) {
      return;
    }
    let active = true;
    const load = async () => {
      try {
        const event = await api.event(appId, selectedEventId);
        if (active) {
          setLoadedEvent({ id: selectedEventId, value: event });
        }
      } catch (error) {
        if (active) {
          notify(errorMessage(error, "Unable to load event"));
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [appId, latestEvent, notify, selectedEventId]);

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

  const loadMore = async () => {
    setLoadingMore(true);
    try {
      const page = await api.issue(appId, detail.issue.id, events.length);
      setEvents((current) => {
        const ids = new Set(current.map((event) => event.event_id));
        return [
          ...current,
          ...page.events.filter((event) => !ids.has(event.event_id)),
        ];
      });
    } catch (error) {
      notify(errorMessage(error, "Unable to load more events"));
    } finally {
      setLoadingMore(false);
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
        <div className="mt-6 flex max-w-5xl flex-wrap gap-x-6 gap-y-4 border-t border-border pt-4">
          <Fact
            label="Events"
            value={
              <span className="flex items-center gap-1.5">
                <Activity className="size-3" />
                {detail.issue.eventCount.toLocaleString()}
              </span>
            }
          />
          <Fact
            label="Users"
            value={
              <span className="flex items-center gap-1.5">
                <Users className="size-3" />
                {detail.userCount.toLocaleString()}
              </span>
            }
          />
          <Fact label="First seen" value={formatTime(detail.issue.firstSeen)} />
          <Fact label="Last seen" value={formatTime(detail.issue.lastSeen)} />
          <Fact label="State" value={<StatusBadge value={status} />} />
        </div>
      </section>

      <section className="flex border-b border-border max-lg:flex-col">
        <IssueActivity detail={detail} />
        <IssueDistributions detail={detail} />
      </section>

      <section className="flex min-h-12 items-center justify-between gap-3 border-b border-border px-5 py-2 lg:px-7">
        <div className="flex items-center gap-1">
          <span className="mr-2 text-[8px] tracking-[0.1em] text-muted-foreground uppercase">
            Event
          </span>
          <Button
            disabled={!firstId}
            onClick={() => setSelectedEventId(firstId)}
            size="sm"
            variant={selectedEventId === firstId ? "secondary" : "ghost"}
          >
            First
          </Button>
          <Button
            disabled={!latestId}
            onClick={() => setSelectedEventId(latestId)}
            size="sm"
            variant={selectedEventId === latestId ? "secondary" : "ghost"}
          >
            Latest
          </Button>
          <Button
            disabled={!recommendedId}
            onClick={() => setSelectedEventId(recommendedId)}
            size="sm"
            variant={selectedEventId === recommendedId ? "secondary" : "ghost"}
          >
            Recommended
          </Button>
        </div>
        <div className="flex items-center gap-1">
          <Button
            aria-label="Newer event"
            disabled={selectedIndex <= 0}
            onClick={() =>
              setSelectedEventId(events[selectedIndex - 1]?.event_id)
            }
            size="icon"
            variant="ghost"
          >
            <ArrowLeft />
          </Button>
          <Button
            aria-label="Older event"
            disabled={
              selectedIndex === -1 || selectedIndex >= events.length - 1
            }
            onClick={() =>
              setSelectedEventId(events[selectedIndex + 1]?.event_id)
            }
            size="icon"
            variant="ghost"
          >
            <ArrowRight />
          </Button>
        </div>
      </section>

      {selectedEvent ? (
        <EventEvidence
          appId={appId}
          detail={selectedEvent}
          label="Selected event"
          notify={notify}
          onOpenTrace={onOpenTrace}
        />
      ) : (
        <div className="px-5 py-10 text-center text-[9px] tracking-[0.12em] text-muted-foreground uppercase lg:px-7">
          Loading event evidence
        </div>
      )}

      <RelatedIssues
        appId={appId}
        event={selectedEvent?.event}
        issue={detail.issue}
        openDetail={openDetail}
      />

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
          {events.map((event) => (
            <button
              className="grid w-full grid-cols-[28px_minmax(0,1fr)_auto] items-center gap-3 py-3 text-left hover:bg-muted/35"
              key={event.event_id}
              onClick={() => setSelectedEventId(event.event_id)}
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
        {events.length < detail.eventTotal ? (
          <div className="flex justify-center border-b border-border py-3">
            <Button
              disabled={loadingMore}
              onClick={() => void loadMore()}
              size="sm"
              variant="ghost"
            >
              {loadingMore ? "Loading…" : "Load more events"}
            </Button>
          </div>
        ) : null}
      </section>
    </>
  );
};
