import { Eyebrow, StatusBadge } from "./common-ui";
import { DetailGrid, DownloadButton } from "./detail-common";
import { asRecord } from "./event-context";
import {
  EventEnvironmentSection,
  EventRequestSection,
} from "./event-context-sections";
import {
  BreadcrumbsSection,
  ContextSection,
  EventMessageSection,
} from "./event-sections";
import { formatTime, shortId } from "./format";
import { RelatedReplay } from "./related-replay";
import type { EventDetail } from "./types";

interface StackFrame {
  colno?: number;
  contextLine?: string;
  filename?: string;
  functionName?: string;
  lineno?: number;
  postContext: string[];
  preContext: string[];
}

const stringArray = (value: unknown): string[] =>
  Array.isArray(value)
    ? value.filter((line): line is string => typeof line === "string")
    : [];

export const framesFromStacktrace = (value: unknown): StackFrame[] => {
  const frames = asRecord(value)?.frames;
  if (!Array.isArray(frames)) {
    return [];
  }
  return frames.flatMap((frame) => {
    const record = asRecord(frame);
    if (!record) {
      return [];
    }
    return [
      {
        colno: typeof record.colno === "number" ? record.colno : undefined,
        contextLine:
          typeof record.context_line === "string"
            ? record.context_line
            : undefined,
        filename:
          typeof record.filename === "string" ? record.filename : undefined,
        functionName:
          typeof record.function === "string" ? record.function : undefined,
        lineno: typeof record.lineno === "number" ? record.lineno : undefined,
        postContext: stringArray(record.post_context),
        preContext: stringArray(record.pre_context),
      },
    ];
  });
};

interface SourceLine {
  active: boolean;
  number?: number;
  text: string;
}

export const sourceContextLines = (frame: StackFrame): SourceLine[] => {
  if (frame.contextLine === undefined) {
    return [];
  }
  const firstLine = frame.lineno
    ? Math.max(1, frame.lineno - frame.preContext.length)
    : undefined;
  return [
    ...frame.preContext.map((text, index) => ({
      active: false,
      number: firstLine === undefined ? undefined : firstLine + index,
      text,
    })),
    {
      active: true,
      number: frame.lineno,
      text: frame.contextLine,
    },
    ...frame.postContext.map((text, index) => ({
      active: false,
      number: frame.lineno === undefined ? undefined : frame.lineno + index + 1,
      text,
    })),
  ];
};

const SourceContext = ({ frame }: { frame: StackFrame }) => {
  const lines = sourceContextLines(frame);
  if (lines.length === 0) {
    return null;
  }
  return (
    <div
      aria-label={`Source context for ${frame.filename ?? "stack frame"}`}
      className="mt-3 overflow-x-auto border-y border-border bg-background"
    >
      <pre className="w-max min-w-full py-1 text-[9px] leading-5">
        {lines.map((line, index) => (
          <span
            className={`grid grid-cols-[4.5rem_minmax(max-content,1fr)] border-l-2 pr-4 ${
              line.active
                ? "border-l-destructive bg-destructive/10 text-foreground"
                : "border-l-transparent text-muted-foreground"
            }`}
            key={`${line.number ?? "unknown"}:${index}`}
          >
            <span className="border-r border-border px-3 text-right text-foreground/40 select-none">
              {line.number ?? "—"}
            </span>
            <code className="pl-4">{line.text || " "}</code>
          </span>
        ))}
      </pre>
    </div>
  );
};

interface StackGroup {
  frames: StackFrame[];
  label: string;
}

const eventStackGroups = (detail: EventDetail): StackGroup[] => {
  const symbolicated = asRecord(detail.symbolication?.payload)?.stacktraces;
  if (Array.isArray(symbolicated)) {
    const groups = symbolicated.flatMap((stacktrace, index) => {
      const frames = framesFromStacktrace(stacktrace);
      return frames.length > 0
        ? [{ frames, label: `Symbolicated stack ${index + 1}` }]
        : [];
    });
    if (groups.length > 0) {
      return groups;
    }
  }
  const exceptions = asRecord(
    asRecord(detail.event.payload)?.exception
  )?.values;
  if (!Array.isArray(exceptions)) {
    return [];
  }
  return exceptions.flatMap((exception, index) => {
    const value = asRecord(exception);
    const frames = framesFromStacktrace(value?.stacktrace);
    if (frames.length === 0) {
      return [];
    }
    const label = [value?.type, value?.value]
      .filter((entry): entry is string => typeof entry === "string")
      .join(": ");
    return [{ frames, label: label || `Exception ${index + 1}` }];
  });
};

const StackTrace = ({ detail }: { detail: EventDetail }) => {
  const groups = eventStackGroups(detail);
  const frameCount = groups.reduce(
    (total, group) => total + group.frames.length,
    0
  );
  if (frameCount === 0) {
    return null;
  }
  return (
    <section className="border-b border-border py-6">
      <div className="mb-4 flex items-center justify-between gap-4">
        <div>
          <Eyebrow>Stack trace</Eyebrow>
          <p className="mt-1.5 text-[10px] text-muted-foreground">
            {frameCount.toLocaleString()} frame{frameCount === 1 ? "" : "s"}
            {groups.length > 1 ? ` across ${groups.length} exceptions` : ""}
          </p>
        </div>
        <StatusBadge value={detail.symbolication ? "symbolicated" : "raw"} />
      </div>
      <div className="border-y border-border">
        {groups.map((group) => (
          <div
            className="border-b border-border last:border-b-0"
            key={group.label}
          >
            <div className="bg-muted/25 px-3 py-2 text-[9px] font-medium [overflow-wrap:anywhere]">
              {group.label}
            </div>
            <div className="divide-y divide-border">
              {group.frames.map((frame, index) => (
                <div
                  className="py-3 text-[10px]"
                  key={`${frame.filename ?? "unknown"}:${frame.lineno ?? 0}:${index}`}
                >
                  <div className="grid grid-cols-[32px_minmax(0,1fr)_auto] items-center gap-3 px-3">
                    <span className="grid size-7 place-items-center border border-border text-[9px] text-muted-foreground">
                      {index + 1}
                    </span>
                    <div className="min-w-0">
                      <p className="overflow-hidden font-medium text-ellipsis whitespace-nowrap">
                        {frame.functionName ?? "<anonymous>"}
                      </p>
                      <code className="mt-1 block overflow-hidden text-[9px] text-ellipsis whitespace-nowrap text-muted-foreground">
                        {frame.filename ?? "unknown source"}
                      </code>
                    </div>
                    <code className="text-[9px] text-muted-foreground">
                      {frame.lineno ?? "—"}:{frame.colno ?? "—"}
                    </code>
                  </div>
                  <SourceContext frame={frame} />
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>
    </section>
  );
};

export const EventEvidence = ({
  appId,
  detail,
  label = "Event evidence",
  notify,
}: {
  appId: string;
  detail: EventDetail;
  label?: string;
  notify: (message: string) => void;
}) => {
  const item = detail.event;
  return (
    <div className="px-5 lg:px-7">
      <section className="border-b border-border py-6">
        <div className="flex items-start justify-between gap-6">
          <div className="min-w-0">
            <Eyebrow>{label}</Eyebrow>
            <p className="mt-2 overflow-hidden text-xs font-medium text-ellipsis whitespace-nowrap">
              {item.event_id ?? "Unknown event"}
            </p>
            <p className="mt-1 text-[9px] text-muted-foreground">
              Received {formatTime(item.received_at)}
            </p>
          </div>
          {item.content_id ? (
            <DownloadButton
              appId={appId}
              contentId={item.content_id}
              filename={`${item.event_id ?? "event"}.json`}
              onError={notify}
            />
          ) : null}
        </div>
      </section>
      <StackTrace detail={detail} />
      {item.replay_id ? (
        <RelatedReplay appId={appId} replayId={item.replay_id} />
      ) : null}
      <BreadcrumbsSection payload={item.payload} />
      <EventMessageSection payload={item.payload} />
      <EventEnvironmentSection payload={item.payload} />
      <EventRequestSection payload={item.payload} />
      <ContextSection payload={item.payload} />
      {item.payload === undefined ? (
        <section className="py-6 text-[10px] text-muted-foreground">
          The indexed event is available above. Download the raw item to inspect
          payload fields that exceeded the inline indexing limit.
        </section>
      ) : null}
    </div>
  );
};

export const EventDetailView = ({
  appId,
  detail,
  notify,
}: {
  appId: string;
  detail: EventDetail;
  notify: (message: string) => void;
}) => {
  const item = detail.event;
  return (
    <>
      <section className="border-b border-border px-5 py-6 lg:px-7">
        <div className="max-w-5xl">
          <div className="flex items-center gap-2">
            <StatusBadge value={item.level} />
            <span className="text-[9px] text-muted-foreground">
              {item.platform ?? "other"}
            </span>
          </div>
          <h1 className="mt-3 text-xl font-medium tracking-[-0.035em]">
            {item.title ?? item.message ?? "Event"}
          </h1>
          <div className="mt-5 max-w-3xl">
            <DetailGrid
              rows={[
                ["Event ID", item.event_id],
                ["Issue", shortId(item.issue_id, 24)],
                [
                  "SDK",
                  [item.sdk_name, item.sdk_version].filter(Boolean).join(" "),
                ],
                ["Environment", item.environment],
                ["Received", formatTime(item.received_at)],
              ]}
            />
          </div>
        </div>
      </section>
      <EventEvidence appId={appId} detail={detail} notify={notify} />
    </>
  );
};
