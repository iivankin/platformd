import type { Replayer as ReplayerInstance } from "@sentry/rrweb";
import { LoaderCircle, Pause, Play, RotateCcw } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";

import { asRecord } from "./event-context";
import { prepareReplayEvents, replayVideoSegments } from "./replay-events";
import { ReplayInspector } from "./replay-inspector";
import { replayMarkers } from "./replay-markers";
import { ReplayTimeline } from "./replay-timeline";
import { ReplayVideoPlayer } from "./replay-video-player";
import type { ReplayRecording } from "./types";

const formatPosition = (milliseconds: number) => {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
};

const replayViewport = (events: ReplayRecording["events"]) => {
  const metadata = events.find((event) => event.type === 4);
  const data = asRecord(metadata?.data);
  return {
    height: typeof data?.height === "number" ? data.height : 720,
    width: typeof data?.width === "number" ? data.width : 1280,
  };
};

export const ReplayPlayer = ({
  onOpenTrace,
  recording,
}: {
  onOpenTrace?: (traceID: string) => void;
  recording: ReplayRecording;
}) => {
  const containerRef = useRef<HTMLDivElement>(null);
  const frameRef = useRef<HTMLIFrameElement>(null);
  const playerRef = useRef<ReplayerInstance>(null);
  const [availableWidth, setAvailableWidth] = useState(0);
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(0);
  const [playerError, setPlayerError] = useState("");
  const [playing, setPlaying] = useState(false);
  const [ready, setReady] = useState(false);
  const [replayFrameReady, setReplayFrameReady] = useState(false);
  const [speed, setSpeed] = useState(1);
  const [startTime, setStartTime] = useState(0);
  const prepared = useMemo(
    () =>
      prepareReplayEvents(recording.events, {
        finishedAt: recording.finishedAt,
        startedAt: recording.startedAt,
      }),
    [recording.events, recording.finishedAt, recording.startedAt]
  );
  const videoSegments = useMemo(
    () => replayVideoSegments(recording.events),
    [recording.events]
  );
  const viewport = replayViewport(prepared.events);
  const markers = useMemo(() => replayMarkers(recording), [recording]);
  const scale = availableWidth
    ? Math.min(1, availableWidth / viewport.width)
    : 1;

  useLayoutEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return;
    }
    const update = () => setAvailableWidth(container.clientWidth);
    update();
    const observer = new ResizeObserver(update);
    observer.observe(container);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    let active = true;
    const mount = replayFrameReady
      ? frameRef.current?.contentDocument?.getElementById("root")
      : null;
    if (!mount || prepared.events.length === 0 || videoSegments.length > 0) {
      return;
    }
    setPlayerError("");
    setReady(false);
    const initialize = async () => {
      try {
        const { Replayer } = await import("@sentry/rrweb");
        if (!active) {
          return;
        }
        mount.replaceChildren();
        const player = new Replayer(prepared.events, {
          UNSAFE_replayCanvas: false,
          mouseTail: false,
          root: mount,
          showWarning: false,
          skipInactive: true,
          speed: 1,
          triggerFocus: false,
        });
        player.on("finish", () => {
          setCurrentTime(player.getMetaData().totalTime);
          setPlaying(false);
        });
        player.on("pause", () => setPlaying(false));
        player.on("resume", () => setPlaying(true));
        player.on("start", () => setPlaying(true));
        playerRef.current = player;
        const metadata = player.getMetaData();
        setDuration(metadata.totalTime);
        setStartTime(metadata.startTime);
        setCurrentTime(0);
        setReady(true);
      } catch (error) {
        if (active) {
          setPlayerError(
            error instanceof Error
              ? error.message
              : "Unable to initialize replay"
          );
        }
      }
    };
    void initialize();
    return () => {
      active = false;
      playerRef.current?.destroy();
      playerRef.current = null;
      mount.replaceChildren();
    };
  }, [prepared, replayFrameReady, videoSegments.length]);

  useEffect(() => {
    if (!playing) {
      return;
    }
    const interval = window.setInterval(() => {
      const player = playerRef.current;
      if (player) {
        setCurrentTime(player.getCurrentTime());
      }
    }, 100);
    return () => window.clearInterval(interval);
  }, [playing]);

  if (videoSegments.length > 0) {
    return (
      <ReplayVideoPlayer
        onOpenTrace={onOpenTrace}
        recording={recording}
        segments={videoSegments}
      />
    );
  }

  if (prepared.events.length === 0) {
    return (
      <p className="border-y border-border py-8 text-center text-[10px] text-muted-foreground">
        This replay has no recorded browser events.
      </p>
    );
  }

  const seek = (position: number) => {
    const player = playerRef.current;
    if (!player) {
      return;
    }
    const resume = playing;
    player.pause(position);
    if (resume) {
      player.play(position);
    }
    setCurrentTime(position);
  };
  const togglePlayback = () => {
    const player = playerRef.current;
    if (!player) {
      return;
    }
    if (playing) {
      player.pause();
      setCurrentTime(player.getCurrentTime());
      return;
    }
    const position = currentTime >= duration ? 0 : currentTime;
    player.play(position);
    setCurrentTime(position);
  };
  const cycleSpeed = () => {
    let next = 1;
    if (speed === 1) {
      next = 2;
    } else if (speed === 2) {
      next = 4;
    }
    playerRef.current?.setConfig({ speed: next });
    setSpeed(next);
  };

  return (
    <div className="grid border-y border-border lg:grid-cols-[minmax(0,1fr)_minmax(22rem,0.38fr)]">
      <div className="min-w-0 bg-muted/15">
        <div
          className="relative overflow-hidden bg-muted/35"
          ref={containerRef}
        >
          {!ready && !playerError ? (
            <div className="absolute inset-0 z-10 grid place-items-center text-[9px] tracking-[0.12em] text-stone-400 uppercase">
              <span className="flex items-center gap-2">
                <LoaderCircle className="size-3.5 animate-spin" /> Preparing
                replay
              </span>
            </div>
          ) : null}
          {playerError ? (
            <div className="absolute inset-0 z-10 grid place-items-center p-6 text-center text-[10px] text-red-300">
              {playerError}
            </div>
          ) : null}
          <div
            className="relative mx-auto overflow-hidden border-x border-border/70 bg-background shadow-[0_0_32px_rgba(0,0,0,0.2)]"
            style={{
              height: viewport.height * scale,
              width: viewport.width * scale,
            }}
          >
            <iframe
              className="absolute top-0 left-0 origin-top-left border-0 bg-white"
              onLoad={() => setReplayFrameReady(true)}
              ref={frameRef}
              sandbox="allow-same-origin"
              src="/replay-frame.html"
              style={{
                height: viewport.height,
                transform: `scale(${scale})`,
                width: viewport.width,
              }}
              title="Session replay"
            />
          </div>
        </div>
        <div className="flex items-center gap-3 border-t border-border bg-background px-3 py-2">
          <Button
            aria-label={playing ? "Pause replay" : "Play replay"}
            disabled={!ready}
            onClick={togglePlayback}
            size="icon"
            variant="outline"
          >
            {playing ? <Pause /> : <Play />}
          </Button>
          <Button
            aria-label="Restart replay"
            disabled={!ready}
            onClick={() => seek(0)}
            size="icon"
            variant="ghost"
          >
            <RotateCcw />
          </Button>
          <span className="w-20 text-[9px] text-muted-foreground tabular-nums">
            {formatPosition(currentTime)} / {formatPosition(duration)}
          </span>
          <ReplayTimeline
            currentTime={currentTime}
            disabled={!ready}
            duration={duration}
            markers={markers}
            onSeek={seek}
            startTime={startTime}
          />
          <Button
            disabled={!ready}
            onClick={cycleSpeed}
            size="sm"
            variant="ghost"
          >
            {speed}×
          </Button>
        </div>
      </div>
      <ReplayInspector
        currentTimestamp={startTime + currentTime}
        onOpenTrace={onOpenTrace}
        onSeek={(timestamp) => seek(Math.max(0, timestamp - startTime))}
        recording={recording}
        startTime={startTime || prepared.startTime}
      />
    </div>
  );
};
