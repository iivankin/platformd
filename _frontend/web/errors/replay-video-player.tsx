import type { Replayer as ReplayerInstance } from "@sentry/rrweb";
import { LoaderCircle, Pause, Play, RotateCcw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";

import { api } from "./api";
import { replayInteractionEvents } from "./replay-events";
import { ReplayInspector } from "./replay-inspector";
import { replayMarkers } from "./replay-markers";
import { ReplayTimeline } from "./replay-timeline";
import type { ReplayTimelineGap } from "./replay-timeline";
import type { ReplayRecording, ReplayVideoSegment } from "./types";

const formatPosition = (milliseconds: number) => {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
};

export const replayVideoSegmentAt = (
  segments: ReplayVideoSegment[],
  timestamp: number
) => {
  const exact = segments.findIndex(
    (segment) =>
      timestamp >= segment.timestamp &&
      timestamp <= segment.timestamp + segment.duration
  );
  if (exact !== -1) {
    return exact;
  }
  return segments.findLastIndex((segment) => segment.timestamp <= timestamp);
};

export const replayVideoGaps = (
  segments: ReplayVideoSegment[],
  startTime: number,
  endTime: number
): ReplayTimelineGap[] => {
  const gaps: ReplayTimelineGap[] = [];
  let previousEnd = startTime;
  for (const segment of segments.toSorted(
    (left, right) => left.timestamp - right.timestamp
  )) {
    if (segment.timestamp - previousEnd > 1100) {
      gaps.push({
        end: segment.timestamp - startTime,
        start: Math.max(0, previousEnd - startTime),
      });
    }
    previousEnd = Math.max(previousEnd, segment.timestamp + segment.duration);
  }
  if (previousEnd < endTime) {
    gaps.push({
      end: endTime - startTime,
      start: Math.max(0, previousEnd - startTime),
    });
  }
  return gaps;
};

export const ReplayVideoPlayer = ({
  onOpenTrace,
  recording,
  segments,
}: {
  onOpenTrace?: (traceID: string) => void;
  recording: ReplayRecording;
  segments: ReplayVideoSegment[];
}) => {
  const videoRef = useRef<HTMLVideoElement>(null);
  const interactionMountRef = useRef<HTMLDivElement>(null);
  const interactionPlayerRef = useRef<ReplayerInstance>(null);
  const currentTimeRef = useRef(0);
  const startTime = recording.startedAt ?? segments[0]?.timestamp ?? 0;
  const endTime = Math.max(
    recording.finishedAt ?? 0,
    ...segments.map((segment) => segment.timestamp + segment.duration)
  );
  const duration = Math.max(0, endTime - startTime);
  const [currentTime, setCurrentTime] = useState(0);
  const [playing, setPlaying] = useState(false);
  const [readySegmentId, setReadySegmentId] = useState<number>();
  const [speed, setSpeed] = useState(1);
  const absoluteTime = startTime + currentTime;
  const activeIndex = Math.max(0, replayVideoSegmentAt(segments, absoluteTime));
  const segment = segments[activeIndex];
  const ready = readySegmentId === segment?.id;
  const initialized = readySegmentId !== undefined;
  const unavailableRanges = useMemo(
    () => replayVideoGaps(segments, startTime, endTime),
    [endTime, segments, startTime]
  );
  const markers = useMemo(() => replayMarkers(recording), [recording]);
  const interactionEvents = useMemo(
    () => replayInteractionEvents(recording.events),
    [recording.events]
  );

  const resizeInteractions = useCallback(() => {
    const video = videoRef.current;
    const mount = interactionMountRef.current;
    const wrapper = mount?.querySelector<HTMLElement>(".replayer-wrapper");
    const metadata = interactionEvents.find((event) => event.type === 4);
    const data = metadata?.data as
      | { height?: unknown; width?: unknown }
      | undefined;
    if (!(video && mount && wrapper)) {
      return;
    }
    const sourceWidth =
      typeof data?.width === "number" ? data.width : video.videoWidth;
    const sourceHeight =
      typeof data?.height === "number" ? data.height : video.videoHeight;
    if (!(sourceWidth > 0 && sourceHeight > 0)) {
      return;
    }
    const bounds = video.getBoundingClientRect();
    wrapper.style.height = `${sourceHeight}px`;
    wrapper.style.transform = `scale(${bounds.width / sourceWidth}, ${bounds.height / sourceHeight})`;
    wrapper.style.transformOrigin = "top left";
    wrapper.style.width = `${sourceWidth}px`;
  }, [interactionEvents]);

  const syncVideo = useCallback(() => {
    const video = videoRef.current;
    if (!video || !segment || video.readyState === 0) {
      return;
    }
    const offset = Math.min(
      segment.duration,
      Math.max(0, absoluteTime - segment.timestamp)
    );
    if (Math.abs(video.currentTime * 1000 - offset) > 250) {
      video.currentTime = offset / 1000;
    }
    video.playbackRate = speed;
    const insideSegment =
      absoluteTime >= segment.timestamp &&
      absoluteTime < segment.timestamp + segment.duration;
    if (playing && insideSegment) {
      if (video.paused) {
        const play = async () => {
          try {
            await video.play();
          } catch {
            // Segment changes can abort an in-flight play request.
          }
        };
        void play();
      }
    } else {
      video.pause();
    }
  }, [absoluteTime, playing, segment, speed]);

  useEffect(() => syncVideo(), [syncVideo]);

  // Mobile replay video is segmented and may contain dead air. The replay clock
  // must therefore advance independently from any one <video> element.
  useEffect(() => {
    if (!playing) {
      return;
    }
    let frame = 0;
    let previous = performance.now();
    const tick = (now: number) => {
      const elapsed = Math.max(0, now - previous) * speed;
      previous = now;
      const position = Math.min(duration, currentTimeRef.current + elapsed);
      currentTimeRef.current = position;
      setCurrentTime(position);
      if (position >= duration) {
        interactionPlayerRef.current?.pause(duration);
        setPlaying(false);
        return;
      }
      frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [duration, playing, speed]);

  useEffect(() => {
    const mount = interactionMountRef.current;
    if (!mount || interactionEvents.length < 2) {
      return;
    }
    let active = true;
    const initialize = async () => {
      const { Replayer } = await import("@sentry/rrweb");
      if (!active) {
        return;
      }
      mount.replaceChildren();
      const player = new Replayer(interactionEvents, {
        blockClass: "sentry-block",
        mouseTail: {
          duration: 750,
          lineCap: "round",
          lineWidth: 2,
          strokeStyle: "#22d3ee",
        },
        root: mount,
        showWarning: false,
        skipInactive: false,
        speed: 1,
        triggerFocus: false,
      });
      interactionPlayerRef.current = player;
      resizeInteractions();
    };
    void initialize();
    const observer = new ResizeObserver(resizeInteractions);
    if (videoRef.current) {
      observer.observe(videoRef.current);
    }
    return () => {
      active = false;
      observer.disconnect();
      interactionPlayerRef.current?.destroy();
      interactionPlayerRef.current = null;
      mount.replaceChildren();
    };
  }, [interactionEvents, resizeInteractions]);

  const seek = (position: number) => {
    const next = Math.min(duration, Math.max(0, position));
    currentTimeRef.current = next;
    setCurrentTime(next);
    interactionPlayerRef.current?.pause(next);
    if (playing) {
      interactionPlayerRef.current?.play(next);
    }
  };

  const togglePlayback = () => {
    if (playing) {
      videoRef.current?.pause();
      interactionPlayerRef.current?.pause(currentTime);
      setPlaying(false);
      return;
    }
    const position = currentTime >= duration ? 0 : currentTime;
    if (position !== currentTime) {
      currentTimeRef.current = position;
      setCurrentTime(position);
    }
    interactionPlayerRef.current?.play(position);
    setPlaying(true);
  };

  const cycleSpeed = () => {
    let next = 1;
    if (speed === 1) {
      next = 2;
    } else if (speed === 2) {
      next = 4;
    }
    setSpeed(next);
    if (videoRef.current) {
      videoRef.current.playbackRate = next;
    }
    interactionPlayerRef.current?.setConfig({ speed: next });
  };

  return (
    <div className="grid border-y border-border lg:grid-cols-[minmax(0,1fr)_minmax(22rem,0.38fr)]">
      <div className="min-w-0 bg-muted/15">
        <div className="relative grid min-h-96 place-items-center overflow-hidden bg-black">
          {ready ? null : (
            <span className="absolute z-10 flex items-center gap-2 text-[9px] tracking-[0.12em] text-stone-400 uppercase">
              <LoaderCircle className="size-3.5 animate-spin" /> Preparing
              replay
            </span>
          )}
          {segment ? (
            <div className="relative inline-grid max-h-[70vh] max-w-full">
              <video
                className="col-start-1 row-start-1 max-h-[70vh] max-w-full object-contain"
                key={segment.id}
                muted
                onEnded={(event) => event.currentTarget.pause()}
                onLoadedData={() => {
                  setReadySegmentId(segment.id);
                  syncVideo();
                  resizeInteractions();
                }}
                playsInline
                preload="auto"
                ref={videoRef}
                src={`${api.replayVideoUrl(recording.replayId, segment.id)}#t=0.001`}
              />
              <div
                aria-hidden="true"
                className="pointer-events-none absolute inset-0 overflow-hidden [&_.replayer-wrapper]:absolute [&_iframe]:invisible"
                ref={interactionMountRef}
              />
            </div>
          ) : null}
        </div>
        <div className="flex items-center gap-3 border-t border-border bg-background px-3 py-2">
          <Button
            aria-label={playing ? "Pause replay" : "Play replay"}
            disabled={!initialized}
            onClick={togglePlayback}
            size="icon"
            variant="outline"
          >
            {playing ? <Pause /> : <Play />}
          </Button>
          <Button
            aria-label="Restart replay"
            disabled={!initialized}
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
            disabled={!initialized}
            duration={duration}
            markers={markers}
            onSeek={seek}
            startTime={startTime}
            unavailableRanges={unavailableRanges}
          />
          <Button
            disabled={!initialized}
            onClick={cycleSpeed}
            size="sm"
            variant="ghost"
          >
            {speed}×
          </Button>
        </div>
      </div>
      <ReplayInspector
        currentTimestamp={absoluteTime}
        onOpenTrace={onOpenTrace}
        onSeek={(timestamp) => seek(timestamp - startTime)}
        recording={recording}
        startTime={startTime}
      />
    </div>
  );
};
