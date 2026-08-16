import { LoaderCircle, MonitorPlay } from "lucide-react";
import { useEffect, useState } from "react";

import { api } from "./api";
import { Eyebrow, StatusBadge } from "./common-ui";
import { errorMessage, shortId } from "./format";
import { ReplayPlayer } from "./replay-player";
import type { ReplayRecording } from "./types";

export const RelatedReplay = ({
  appId,
  loadRecording,
  onOpenTrace,
  replayId,
}: {
  appId: string;
  loadRecording?: (
    replayId: string,
    signal: AbortSignal
  ) => Promise<ReplayRecording>;
  onOpenTrace?: (traceID: string) => void;
  replayId: string;
}) => {
  const requestKey = `${appId}:${replayId}`;
  const [result, setResult] = useState<{
    error?: string;
    key: string;
    recording?: ReplayRecording;
  }>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const recording = loadRecording
          ? await loadRecording(replayId, controller.signal)
          : await api.replayRecording(appId, replayId);
        if (!controller.signal.aborted) {
          setResult({ key: requestKey, recording });
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setResult({
            error: errorMessage(error, "Unable to load session replay"),
            key: requestKey,
          });
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [appId, loadRecording, replayId, requestKey]);

  const current = result?.key === requestKey ? result : undefined;
  return (
    <section className="border-b border-border py-6">
      <div className="mb-4 flex items-start justify-between gap-4">
        <div>
          <span className="flex items-center gap-2">
            <MonitorPlay className="size-3.5 text-muted-foreground" />
            <Eyebrow>Session replay</Eyebrow>
          </span>
          <p className="mt-1.5 text-[10px] text-muted-foreground">
            Browser state captured around this event · {shortId(replayId, 24)}
          </p>
        </div>
        {current?.recording ? (
          <StatusBadge
            value={`${current.recording.segmentCount} segment${
              current.recording.segmentCount === 1 ? "" : "s"
            }`}
          />
        ) : null}
      </div>
      {current ? null : (
        <div className="grid h-40 place-items-center border-y border-border text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
          <span className="flex items-center gap-2">
            <LoaderCircle className="size-3.5 animate-spin" /> Loading replay
          </span>
        </div>
      )}
      {current?.error ? (
        <p className="border-y border-border py-8 text-center text-[10px] text-muted-foreground">
          {current.error}
        </p>
      ) : null}
      {current?.recording?.warnings?.length || current?.recording?.truncated ? (
        <div className="border-y border-amber-500/30 bg-amber-500/5 px-4 py-2 text-[9px] text-amber-500">
          {current.recording.truncated
            ? "Replay is longer than the browser-safe playback window. Showing the available portion."
            : current.recording.warnings?.join(" · ")}
        </div>
      ) : null}
      {current?.recording ? (
        <ReplayPlayer onOpenTrace={onOpenTrace} recording={current.recording} />
      ) : null}
    </section>
  );
};
