import { Eyebrow, StatusBadge } from "./common-ui";
import { DetailGrid, DownloadButton } from "./detail-common";
import { shortId } from "./format";
import { ReplayPlayer } from "./replay-player";
import type { ReplayDetail, ReplayRecording, StoredDocument } from "./types";

export const ReplayDetailView = ({
  appId,
  detail,
  notify,
  recording,
  replayId,
}: {
  appId: string;
  detail: ReplayDetail;
  notify: (message: string) => void;
  recording: ReplayRecording;
  replayId: string;
}) => (
  <>
    <section className="border-b border-border px-5 py-6 lg:px-7">
      <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
        Session replay
      </p>
      <h1 className="mt-3 text-xl font-medium tracking-[-0.035em]">
        {shortId(replayId, 32)}
      </h1>
      <div className="mt-5 max-w-2xl">
        <DetailGrid
          rows={[
            ["Replay ID", replayId],
            ["Recorded events", recording.events.length.toLocaleString()],
            ["Segments", recording.segmentCount.toLocaleString()],
            ["Envelope items", detail.total],
          ]}
        />
      </div>
    </section>
    <section className="border-b border-border px-5 py-6 lg:px-7">
      <div className="mb-4">
        <Eyebrow>Browser replay</Eyebrow>
        <p className="mt-1.5 text-[10px] text-muted-foreground">
          DOM snapshots and user interactions captured by the Sentry SDK.
        </p>
      </div>
      <ReplayPlayer recording={recording} />
    </section>
    <section className="px-5 py-6 lg:px-7">
      <Eyebrow>Envelope items</Eyebrow>
      <div className="mt-4 max-w-5xl divide-y divide-border border-y border-border">
        {detail.items.map((item: StoredDocument, index) => (
          <div
            className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-3"
            key={`${item.content_id ?? item.doc_kind}-${index}`}
          >
            <div className="min-w-0">
              <StatusBadge value={item.doc_kind} />
              <code className="mt-1.5 block overflow-hidden text-[9px] text-ellipsis whitespace-nowrap text-muted-foreground">
                {shortId(item.content_id, 22)} · segment{" "}
                {item.segment_id ?? "—"}
              </code>
            </div>
            {item.content_id ? (
              <DownloadButton
                appId={appId}
                contentId={item.content_id}
                filename={`${item.content_id}.bin`}
                onError={notify}
              />
            ) : null}
          </div>
        ))}
      </div>
    </section>
  </>
);
