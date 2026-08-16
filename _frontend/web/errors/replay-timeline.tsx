import { Tooltip } from "@base-ui/react/tooltip";
import { useMemo } from "react";

import { cn } from "@/lib/utils";

import type { ReplayMarker, ReplayMarkerKind } from "./replay-markers";

interface PositionedMarker extends ReplayMarker {
  position: number;
}

interface MarkerGroup {
  markers: PositionedMarker[];
  position: number;
}

export interface ReplayTimelineGap {
  end: number;
  start: number;
}

const markerStyles: Record<ReplayMarkerKind, string> = {
  console: "bg-stone-400",
  error: "bg-red-500",
  interaction: "bg-violet-500",
  navigation: "bg-emerald-500",
  network: "bg-cyan-500",
  warning: "bg-amber-500",
};

const markerPriority: ReplayMarkerKind[] = [
  "error",
  "warning",
  "navigation",
  "interaction",
  "network",
  "console",
];

const tooltipCollisionAvoidance = { align: "shift", side: "flip" } as const;
const noTimelineGaps: ReplayTimelineGap[] = [];

const formatPosition = (milliseconds: number) => {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
};

export const groupReplayMarkers = (
  markers: ReplayMarker[],
  startTime: number,
  duration: number
): MarkerGroup[] => {
  if (duration <= 0) {
    return [];
  }
  const positioned = markers
    .map((marker) => ({
      ...marker,
      position: Math.min(
        100,
        Math.max(0, ((marker.timestamp - startTime) / duration) * 100)
      ),
    }))
    .toSorted((left, right) => left.position - right.position);
  const groups: MarkerGroup[] = [];
  for (const marker of positioned) {
    const group = groups.at(-1);
    if (group && marker.position - group.position <= 1.75) {
      group.markers.push(marker);
      group.position =
        group.markers.reduce((sum, item) => sum + item.position, 0) /
        group.markers.length;
    } else {
      groups.push({ markers: [marker], position: marker.position });
    }
  }
  return groups;
};

const MarkerDot = ({ markers }: { markers: ReplayMarker[] }) => {
  const kinds = markerPriority.filter((kind) =>
    markers.some((marker) => marker.kind === kind)
  );
  const [primary, secondary, tertiary] = kinds;
  if (!primary) {
    return null;
  }
  if (!secondary) {
    return (
      <span
        className={cn(
          "block size-2.5 rounded-full ring-2 ring-background",
          markerStyles[primary]
        )}
      />
    );
  }
  return (
    <span
      className={cn(
        "grid size-4 place-items-center rounded-full ring-2 ring-background",
        markerStyles[primary]
      )}
    >
      <span
        className={cn(
          "grid size-2.5 place-items-center rounded-full",
          markerStyles[secondary]
        )}
      >
        {tertiary ? (
          <span
            className={cn("block size-1 rounded-full", markerStyles[tertiary])}
          />
        ) : null}
      </span>
    </span>
  );
};

export const ReplayTimeline = ({
  currentTime,
  disabled,
  duration,
  markers,
  onSeek,
  startTime,
  unavailableRanges = noTimelineGaps,
}: {
  currentTime: number;
  disabled: boolean;
  duration: number;
  markers: ReplayMarker[];
  onSeek: (position: number) => void;
  startTime: number;
  unavailableRanges?: ReplayTimelineGap[];
}) => {
  const groups = useMemo(
    () => groupReplayMarkers(markers, startTime, duration),
    [duration, markers, startTime]
  );
  const played = duration > 0 ? (currentTime / duration) * 100 : 0;

  return (
    <div className="relative h-8 min-w-24 flex-1">
      {unavailableRanges.map((range) => {
        const left = duration > 0 ? (range.start / duration) * 100 : 0;
        const width =
          duration > 0 ? ((range.end - range.start) / duration) * 100 : 0;
        return (
          <span
            className="pointer-events-none absolute top-1/2 h-5 -translate-y-1/2 bg-muted-foreground/15"
            key={`${range.start}:${range.end}`}
            style={{ left: `${left}%`, width: `${width}%` }}
            title="Video unavailable"
          />
        );
      })}
      <div className="absolute inset-x-0 top-1/2 h-px -translate-y-1/2 bg-border" />
      <div
        className="absolute top-1/2 left-0 h-px -translate-y-1/2 bg-foreground"
        style={{ width: `${Math.min(100, Math.max(0, played))}%` }}
      />
      <div
        className="pointer-events-none absolute top-1/2 z-10 h-3 w-px -translate-y-1/2 bg-foreground"
        style={{ left: `${Math.min(100, Math.max(0, played))}%` }}
      />
      <input
        aria-label="Replay position"
        className="absolute inset-0 z-20 size-full cursor-pointer opacity-0"
        disabled={disabled}
        max={Math.max(duration, 1)}
        min={0}
        onChange={(event) => onSeek(Number(event.currentTarget.value))}
        step={100}
        type="range"
        value={Math.min(currentTime, Math.max(duration, 1))}
      />
      <Tooltip.Provider delay={150}>
        {groups.map((group) => {
          const [firstMarker] = group.markers;
          if (!firstMarker) {
            return null;
          }
          const seekTime = Math.min(
            duration,
            Math.max(0, firstMarker.timestamp - startTime)
          );
          const visible = group.markers.slice(0, 5);
          const label = group.markers.map((marker) => marker.label).join(", ");
          return (
            <Tooltip.Root key={`${group.position}-${label}`}>
              <Tooltip.Trigger
                aria-label={`Seek to ${formatPosition(seekTime)}: ${label}`}
                className="absolute top-1/2 z-30 grid size-5 -translate-x-1/2 -translate-y-1/2 place-items-center outline-none focus-visible:ring-2 focus-visible:ring-ring"
                closeOnClick={false}
                onClick={() => onSeek(seekTime)}
                style={{ left: `${group.position}%` }}
              >
                <MarkerDot markers={group.markers} />
              </Tooltip.Trigger>
              <Tooltip.Portal>
                <Tooltip.Positioner
                  className="isolate z-50"
                  collisionAvoidance={tooltipCollisionAvoidance}
                  collisionPadding={8}
                  positionMethod="fixed"
                  side="top"
                  sideOffset={6}
                >
                  <Tooltip.Popup className="w-64 max-w-[calc(100vw-1rem)] border border-border bg-popover px-2.5 py-2 text-left text-[9px] text-popover-foreground shadow-lg outline-none">
                    <span className="mb-1.5 block font-medium text-foreground tabular-nums">
                      {formatPosition(seekTime)} · {group.markers.length} event
                      {group.markers.length === 1 ? "" : "s"}
                    </span>
                    {visible.map((marker, index) => (
                      <span
                        className="flex gap-2 border-t border-border/60 py-1 first:border-t-0"
                        key={`${marker.timestamp}-${marker.kind}-${index}`}
                      >
                        <span
                          className={cn(
                            "mt-1 block size-1.5 shrink-0 rounded-full",
                            markerStyles[marker.kind]
                          )}
                        />
                        <span className="min-w-0">
                          <span className="block [overflow-wrap:anywhere] text-foreground">
                            {marker.label}
                          </span>
                          {marker.detail && marker.detail !== marker.label ? (
                            <span className="block [overflow-wrap:anywhere] text-muted-foreground">
                              {marker.detail}
                            </span>
                          ) : null}
                        </span>
                      </span>
                    ))}
                    {group.markers.length > visible.length ? (
                      <span className="block pt-1 text-muted-foreground">
                        +{group.markers.length - visible.length} more
                      </span>
                    ) : null}
                  </Tooltip.Popup>
                </Tooltip.Positioner>
              </Tooltip.Portal>
            </Tooltip.Root>
          );
        })}
      </Tooltip.Provider>
    </div>
  );
};
