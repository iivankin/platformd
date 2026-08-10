import { LoaderCircle, MonitorPlay } from "lucide-react";
import { useEffect, useState } from "react";

import { api } from "./api";
import { Eyebrow, StatusBadge } from "./common-ui";
import { errorMessage, shortId } from "./format";
import { ReplayPlayer } from "./replay-player";
import type { ReplayRecording } from "./types";

export const RelatedReplay = ({
  appId,
  replayId,
}: {
  appId: string;
  replayId: string;
}) => {
  const requestKey = `${appId}:${replayId}`;
  const [result, setResult] = useState<{
    error?: string;
    key: string;
    recording?: ReplayRecording;
  }>();

  useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        const recording = await api.replayRecording(appId, replayId);
        if (active) {
          setResult({ key: requestKey, recording });
        }
      } catch (error) {
        if (active) {
          setResult({
            error: errorMessage(error, "Unable to load session replay"),
            key: requestKey,
          });
        }
      }
    };
    void load();
    return () => {
      active = false;
    };
  }, [appId, replayId, requestKey]);

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
      {current?.recording ? (
        <ReplayPlayer recording={current.recording} />
      ) : null}
    </section>
  );
};
