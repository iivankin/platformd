import { Flame } from "lucide-react";
import { useMemo, useState } from "react";

import type {
  ServiceTraceProfile,
  ServiceTraceProfileStack,
  ServiceTraceSpan,
} from "@/api";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

interface ProfileNode {
  children: Map<string, ProfileNode>;
  detail: string;
  duration: bigint;
  id: string;
  label: string;
  samples: number;
}

interface FlameSegment {
  depth: number;
  node: ProfileNode;
  width: number;
  x: number;
}

const frameText = (
  frame: Record<string, unknown>,
  keys: string[]
): string | undefined => {
  for (const key of keys) {
    const value = frame[key];
    if (typeof value === "string" && value) {
      return value;
    }
    if (typeof value === "number") {
      return String(value);
    }
  }
};

const framePresentation = (frame: Record<string, unknown>) => {
  const label =
    frameText(frame, ["function", "symbol", "name", "instruction_addr"]) ??
    "unknown frame";
  const file = frameText(frame, [
    "filename",
    "abs_path",
    "package",
    "module",
    "image_name",
  ]);
  const line = frameText(frame, ["lineno", "line"]);
  return {
    detail: [file, line].filter(Boolean).join(":"),
    label,
  };
};

const buildProfileTree = (stacks: ServiceTraceProfileStack[]) => {
  const root: ProfileNode = {
    children: new Map(),
    detail: "",
    duration: 0n,
    id: "root",
    label: "root",
    samples: 0,
  };
  for (const stack of stacks) {
    const duration = BigInt(stack.durationNano);
    root.duration += duration;
    root.samples += stack.sampleCount;
    let parent = root;
    for (const [index, frame] of stack.frames.entries()) {
      const presentation = framePresentation(frame);
      const key = `${presentation.label}\0${presentation.detail}`;
      let child = parent.children.get(key);
      if (!child) {
        child = {
          children: new Map(),
          detail: presentation.detail,
          duration: 0n,
          id: `${parent.id}/${index}:${key}`,
          label: presentation.label,
          samples: 0,
        };
        parent.children.set(key, child);
      }
      child.duration += duration;
      child.samples += stack.sampleCount;
      parent = child;
    }
  }
  return root;
};

const layoutProfileTree = (root: ProfileNode): FlameSegment[] => {
  const segments: FlameSegment[] = [];
  const walk = (
    parent: ProfileNode,
    x: number,
    width: number,
    depth: number
  ) => {
    if (parent.duration === 0n) {
      return;
    }
    let offset = x;
    const children = [...parent.children.values()].toSorted((left, right) => {
      if (left.duration === right.duration) {
        return 0;
      }
      return left.duration > right.duration ? -1 : 1;
    });
    for (const node of children) {
      const nodeWidth =
        width * (Number(node.duration) / Number(parent.duration));
      if (nodeWidth < 0.08) {
        continue;
      }
      segments.push({ depth, node, width: nodeWidth, x: offset });
      walk(node, offset, nodeWidth, depth + 1);
      offset += nodeWidth;
    }
  };
  walk(root, 0, 100, 0);
  return segments;
};

const formatProfileDuration = (duration: bigint) => {
  const milliseconds = Number(duration) / 1_000_000;
  if (milliseconds < 1) {
    return `${Math.round(milliseconds * 1000)} μs`;
  }
  if (milliseconds < 1000) {
    return `${milliseconds.toFixed(milliseconds < 10 ? 2 : 1)} ms`;
  }
  return `${(milliseconds / 1000).toFixed(2)} s`;
};

const all = "__all__";

export const TraceProfileFlamegraph = ({
  profiles,
  spans,
}: {
  profiles: ServiceTraceProfile[];
  spans: ServiceTraceSpan[];
}) => {
  const [profileID, setProfileID] = useState(profiles[0]?.profileId ?? "");
  const [spanID, setSpanID] = useState(all);
  const [threadID, setThreadID] = useState(all);
  const [selectedID, setSelectedID] = useState("");
  const profile =
    profiles.find((candidate) => candidate.profileId === profileID) ??
    profiles[0];
  const threads = useMemo(() => {
    const values = new Map<string, string>();
    for (const stack of profile?.stacks ?? []) {
      values.set(stack.threadId, stack.threadName || stack.threadId);
    }
    return [...values.entries()];
  }, [profile]);
  const linkedSpans = useMemo(() => {
    const values = new Set(
      (profile?.stacks ?? []).map((stack) => stack.spanId).filter(Boolean)
    );
    return spans.filter((span) => values.has(span.spanId));
  }, [profile, spans]);
  const filtered = useMemo(
    () =>
      (profile?.stacks ?? []).filter(
        (stack) =>
          (threadID === all || stack.threadId === threadID) &&
          (spanID === all || stack.spanId === spanID)
      ),
    [profile, spanID, threadID]
  );
  const tree = useMemo(() => buildProfileTree(filtered), [filtered]);
  const segments = useMemo(() => layoutProfileTree(tree), [tree]);
  const selected = segments.find((segment) => segment.node.id === selectedID);
  const depth = Math.max(1, ...segments.map((segment) => segment.depth + 1));

  if (!profile) {
    return null;
  }
  return (
    <section className="border-b border-border">
      <header className="flex flex-wrap items-center gap-2 px-4 py-3 lg:px-6">
        <span className="mr-auto flex min-w-0 items-center gap-2">
          <Flame className="size-3.5 text-orange-500" />
          <span>
            <span className="block text-[10px] font-medium">Profile</span>
            <span className="block text-[8px] text-muted-foreground">
              {profile.sampleCount.toLocaleString()} samples ·{" "}
              {profile.platform}
            </span>
          </span>
        </span>
        {profiles.length > 1 ? (
          <Select
            onValueChange={(value) => {
              setProfileID(String(value));
              setSelectedID("");
            }}
            value={profile.profileId}
          >
            <SelectTrigger className="h-7 max-w-48 text-[9px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="end">
              {profiles.map((candidate, index) => (
                <SelectItem
                  key={candidate.profileId}
                  value={candidate.profileId}
                >
                  Profile {index + 1} · {candidate.platform}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
        {threads.length > 1 ? (
          <Select
            onValueChange={(value) => setThreadID(String(value))}
            value={threadID}
          >
            <SelectTrigger className="h-7 max-w-48 text-[9px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="end">
              <SelectItem value={all}>All threads</SelectItem>
              {threads.map(([id, label]) => (
                <SelectItem key={id} value={id}>
                  {label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
        {linkedSpans.length > 0 ? (
          <Select
            onValueChange={(value) => setSpanID(String(value))}
            value={spanID}
          >
            <SelectTrigger className="h-7 max-w-56 text-[9px]">
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="end">
              <SelectItem value={all}>Entire trace</SelectItem>
              {linkedSpans.map((span) => (
                <SelectItem key={span.spanId} value={span.spanId}>
                  {span.name}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : null}
      </header>
      <div className="overflow-x-auto border-t border-border bg-muted/10 px-4 py-3 lg:px-6">
        <div
          className="relative min-w-[720px]"
          style={{ height: `${depth * 25}px` }}
        >
          {segments.map((segment) => (
            <button
              className={cn(
                "absolute h-6 overflow-hidden border border-background/60 bg-orange-500/70 px-1.5 text-left text-[8px] text-stone-950 hover:bg-orange-400",
                segment.depth % 4 === 1 && "bg-amber-500/70",
                segment.depth % 4 === 2 && "bg-yellow-500/65",
                segment.depth % 4 === 3 && "bg-rose-500/65",
                selectedID === segment.node.id && "z-10 ring-1 ring-foreground"
              )}
              key={segment.node.id}
              onClick={() => setSelectedID(segment.node.id)}
              style={{
                left: `${segment.x}%`,
                top: `${segment.depth * 25}px`,
                width: `${segment.width}%`,
              }}
              title={`${segment.node.label}\n${segment.node.detail}\n${formatProfileDuration(segment.node.duration)} · ${segment.node.samples} samples`}
              type="button"
            >
              <span className="block truncate">{segment.node.label}</span>
            </button>
          ))}
        </div>
      </div>
      <div className="flex min-h-9 flex-wrap items-center gap-x-5 gap-y-1 border-t border-border px-4 py-2 text-[8px] lg:px-6">
        {selected ? (
          <>
            <span className="font-medium">{selected.node.label}</span>
            {selected.node.detail ? (
              <span className="text-muted-foreground">
                {selected.node.detail}
              </span>
            ) : null}
            <span className="text-muted-foreground tabular-nums">
              {formatProfileDuration(selected.node.duration)} ·{" "}
              {selected.node.samples.toLocaleString()} samples
            </span>
          </>
        ) : (
          <span className="text-muted-foreground">
            Select a frame to inspect its aggregated CPU time.
          </span>
        )}
      </div>
    </section>
  );
};
