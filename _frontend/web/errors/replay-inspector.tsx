import {
  Activity,
  Braces,
  ChartNoAxesColumnIncreasing,
  Network,
  Workflow,
} from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

import { replayInspectorData } from "./replay-inspector-data";
import type { ReplayInspectorItem } from "./replay-inspector-data";
import type { ReplayRecording } from "./types";

type ReplayTab = "activity" | "console" | "memory" | "network" | "trace";

const tabs = [
  { icon: Activity, key: "activity", label: "Activity" },
  { icon: Braces, key: "console", label: "Console" },
  { icon: Network, key: "network", label: "Network" },
  { icon: Workflow, key: "trace", label: "Trace" },
  {
    icon: ChartNoAxesColumnIncreasing,
    key: "memory",
    label: "Memory",
  },
] as const;

const formatOffset = (timestamp: number, start: number) => {
  const total = Math.max(0, timestamp - start);
  const seconds = Math.floor(total / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
};

const formatBytes = (bytes: number) => {
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KiB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
};

const EmptyPanel = ({ children }: { children: string }) => (
  <p className="grid min-h-40 place-items-center px-5 text-center text-[9px] text-muted-foreground">
    {children}
  </p>
);

const EventRows = ({
  currentTimestamp,
  items,
  onSeek,
  startTime,
}: {
  currentTimestamp: number;
  items: ReplayInspectorItem[];
  onSeek: (timestamp: number) => void;
  startTime: number;
}) =>
  items.length === 0 ? (
    <EmptyPanel>No events were captured for this view.</EmptyPanel>
  ) : (
    <div className="divide-y divide-border/70">
      {items.map((item, index) => (
        <button
          className={cn(
            "grid w-full grid-cols-[2.75rem_minmax(0,1fr)] gap-2 px-3 py-2.5 text-left hover:bg-muted/30",
            Math.abs(currentTimestamp - item.timestamp) < 500 && "bg-muted/35"
          )}
          key={`${item.timestamp}:${item.title}:${index}`}
          onClick={() => onSeek(item.timestamp)}
          type="button"
        >
          <span className="text-[8px] text-muted-foreground tabular-nums">
            {formatOffset(item.timestamp, startTime)}
          </span>
          <span className="min-w-0">
            <span
              className={cn(
                "block truncate text-[9px]",
                ["error", "fatal"].includes(item.level ?? "") &&
                  "text-destructive",
                item.level === "warning" && "text-amber-500"
              )}
            >
              {item.title}
            </span>
            {item.detail || item.meta ? (
              <span className="mt-0.5 flex min-w-0 gap-2 text-[8px] text-muted-foreground">
                <span className="truncate">{item.detail}</span>
                {item.meta ? (
                  <span className="shrink-0">{item.meta}</span>
                ) : null}
              </span>
            ) : null}
          </span>
        </button>
      ))}
    </div>
  );

export const ReplayInspector = ({
  currentTimestamp,
  onOpenTrace,
  onSeek,
  recording,
  startTime,
}: {
  currentTimestamp: number;
  onOpenTrace?: (traceID: string) => void;
  onSeek: (timestamp: number) => void;
  recording: ReplayRecording;
  startTime: number;
}) => {
  const data = useMemo(() => replayInspectorData(recording), [recording]);
  const [tab, setTab] = useState<ReplayTab>("activity");
  const counts: Record<ReplayTab, number> = {
    activity: data.activity.length,
    console: data.console.length,
    memory: data.memory.length,
    network: data.network.length,
    trace: data.traceIDs.length,
  };
  const memoryMaximum = Math.max(
    1,
    ...data.memory.map((point) =>
      Math.max(point.heapLimit ?? 0, point.usedHeap)
    )
  );
  return (
    <aside className="min-h-0 border-l border-border bg-background max-lg:border-t max-lg:border-l-0">
      <nav className="flex h-10 items-end overflow-x-auto border-b border-border px-2">
        {tabs.map(({ icon: Icon, key, label }) => (
          <button
            className={cn(
              "flex h-full shrink-0 items-center gap-1.5 border-b-2 border-transparent px-2 text-[8px] text-muted-foreground hover:text-foreground",
              tab === key && "border-foreground text-foreground"
            )}
            key={key}
            onClick={() => setTab(key)}
            type="button"
          >
            <Icon className="size-3" /> {label}
            {counts[key] > 0 ? (
              <span className="text-[7px] tabular-nums">{counts[key]}</span>
            ) : null}
          </button>
        ))}
      </nav>
      <div className="max-h-[34rem] overflow-y-auto">
        {tab === "activity" ? (
          <EventRows
            currentTimestamp={currentTimestamp}
            items={data.activity}
            onSeek={onSeek}
            startTime={startTime}
          />
        ) : null}
        {tab === "console" ? (
          <EventRows
            currentTimestamp={currentTimestamp}
            items={data.console}
            onSeek={onSeek}
            startTime={startTime}
          />
        ) : null}
        {tab === "network" ? (
          <EventRows
            currentTimestamp={currentTimestamp}
            items={data.network}
            onSeek={onSeek}
            startTime={startTime}
          />
        ) : null}
        {tab === "trace" && data.traceIDs.length > 0 ? (
          <div className="divide-y divide-border/70">
            {data.traceIDs.map((traceID) => (
              <div
                className="flex items-center gap-2 px-3 py-2.5"
                key={traceID}
              >
                <code className="min-w-0 flex-1 truncate text-[8px]">
                  {traceID}
                </code>
                {onOpenTrace ? (
                  <Button
                    onClick={() => onOpenTrace(traceID)}
                    size="sm"
                    variant="ghost"
                  >
                    Open trace
                  </Button>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}
        {tab === "trace" && data.traceIDs.length === 0 ? (
          <EmptyPanel>
            No trace context was captured for this replay.
          </EmptyPanel>
        ) : null}
        {tab === "memory" && data.memory.length > 0 ? (
          <div>
            <div className="flex h-40 items-end gap-px border-b border-border px-3 pt-4">
              {data.memory.map((point, index) => (
                <button
                  aria-label={`Seek to memory sample at ${formatOffset(point.timestamp, startTime)}`}
                  className="min-w-px flex-1 bg-violet-500/65 hover:bg-violet-400"
                  key={`${point.timestamp}:${index}`}
                  onClick={() => onSeek(point.timestamp)}
                  style={{
                    height: `${Math.max(2, (point.usedHeap / memoryMaximum) * 100)}%`,
                  }}
                  title={`${formatOffset(point.timestamp, startTime)} · ${formatBytes(point.usedHeap)}`}
                  type="button"
                />
              ))}
            </div>
            <div className="grid grid-cols-3 divide-x divide-border text-[8px]">
              {[
                ["Used", data.memory.at(-1)?.usedHeap],
                ["Allocated", data.memory.at(-1)?.totalHeap],
                ["Limit", data.memory.at(-1)?.heapLimit],
              ].map(([label, value]) => (
                <span className="px-3 py-2" key={label}>
                  <span className="block text-muted-foreground">{label}</span>
                  <span className="mt-0.5 block tabular-nums">
                    {typeof value === "number" ? formatBytes(value) : "—"}
                  </span>
                </span>
              ))}
            </div>
          </div>
        ) : null}
        {tab === "memory" && data.memory.length === 0 ? (
          <EmptyPanel>
            Memory samples were not captured. Browser heap data is available
            only when the SDK and browser provide it.
          </EmptyPanel>
        ) : null}
      </div>
    </aside>
  );
};
