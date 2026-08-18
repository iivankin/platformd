import { Check, Clock3 } from "lucide-react";
import { useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { DateTimePicker } from "@/date-time-picker";
import { cn } from "@/lib/utils";
import type { TelemetryTimeRange } from "@/telemetry-query-state";

export interface TelemetryTimeRangeState {
  from: number | null;
  range: TelemetryTimeRange;
  to: number | null;
}

const presets: {
  label: string;
  milliseconds?: number;
  value: Exclude<TelemetryTimeRange, "custom">;
}[] = [
  { label: "All loaded", value: "all" },
  { label: "Today", value: "today" },
  { label: "Last 15 minutes", milliseconds: 15 * 60_000, value: "15m" },
  { label: "Last hour", milliseconds: 60 * 60_000, value: "1h" },
  { label: "Last 6 hours", milliseconds: 6 * 60 * 60_000, value: "6h" },
  { label: "Last 24 hours", milliseconds: 24 * 60 * 60_000, value: "24h" },
  { label: "Last 7 days", milliseconds: 7 * 24 * 60 * 60_000, value: "7d" },
  { label: "Last 28 days", milliseconds: 28 * 24 * 60 * 60_000, value: "28d" },
  { label: "Last 91 days", milliseconds: 91 * 24 * 60 * 60_000, value: "91d" },
  {
    label: "Last 12 months",
    milliseconds: 365 * 24 * 60 * 60_000,
    value: "12m",
  },
];

const presetMilliseconds = Object.fromEntries(
  presets.flatMap((preset) =>
    preset.milliseconds === undefined
      ? []
      : [[preset.value, preset.milliseconds]]
  )
) as Partial<Record<TelemetryTimeRange, number>>;

export const telemetryTimeBounds = (
  state: TelemetryTimeRangeState,
  now = Date.now()
): { from?: number; to?: number } => {
  if (
    state.range === "custom" &&
    state.from !== null &&
    state.to !== null &&
    state.to > state.from
  ) {
    return { from: state.from, to: state.to };
  }
  if (state.range === "today") {
    const start = new Date(now);
    start.setHours(0, 0, 0, 0);
    return { from: start.getTime(), to: now };
  }
  const duration = presetMilliseconds[state.range];
  return duration === undefined ? {} : { from: now - duration, to: now };
};

export const formatTelemetryRange = (
  from: number,
  to: number,
  locale?: Intl.LocalesArgument
) => {
  const showSeconds = to - from < 60_000;
  const showMilliseconds = to - from < 1000;
  return new Intl.DateTimeFormat(locale, {
    day: "numeric",
    fractionalSecondDigits: showMilliseconds ? 3 : undefined,
    hour: "numeric",
    minute: "2-digit",
    month: "short",
    second: showSeconds ? "2-digit" : undefined,
    year: "2-digit",
  }).formatRange(new Date(from), new Date(to));
};

const rangeLabel = (state: TelemetryTimeRangeState) => {
  if (state.range !== "custom") {
    return presets.find((preset) => preset.value === state.range)?.label;
  }
  const bounds = telemetryTimeBounds(state);
  if (bounds.from === undefined || bounds.to === undefined) {
    return "Custom range";
  }
  return formatTelemetryRange(bounds.from, bounds.to);
};

export const TelemetryTimeRangePicker = ({
  onChange,
  value,
}: {
  onChange: (value: TelemetryTimeRangeState) => void;
  value: TelemetryTimeRangeState;
}) => {
  const [error, setError] = useState("");
  const [open, setOpen] = useState(false);
  const nestedOpen = useRef(false);
  const [draftFrom, setDraftFrom] = useState(value.from ?? 0);
  const [draftTo, setDraftTo] = useState(value.to ?? 0);

  const choosePreset = (range: Exclude<TelemetryTimeRange, "custom">) => {
    onChange({ from: null, range, to: null });
    setError("");
    setOpen(false);
  };

  return (
    <Popover
      onOpenChange={(nextOpen) => {
        if (!nextOpen && nestedOpen.current) {
          return;
        }
        setOpen(nextOpen);
        if (nextOpen) {
          const nextTo = value.to ?? Date.now();
          setDraftTo(nextTo);
          setDraftFrom(value.from ?? nextTo - 60 * 60_000);
        }
        if (!nextOpen) {
          setError("");
        }
      }}
      open={open}
    >
      <PopoverTrigger
        render={
          <Button
            aria-label="Telemetry time range"
            className="max-w-56 justify-start font-normal"
            size="sm"
            type="button"
            variant="outline"
          >
            <Clock3 />
            <span className="truncate">{rangeLabel(value)}</span>
          </Button>
        }
      />
      <PopoverContent align="end" className="w-80 gap-0 p-0">
        <div className="grid grid-cols-2 border-b border-border p-2">
          {presets.map((preset) => (
            <button
              className={cn(
                "flex h-8 items-center justify-between px-2 text-left text-[9px] hover:bg-muted/50",
                value.range === preset.value && "bg-muted/55 text-foreground"
              )}
              key={preset.value}
              onClick={() => choosePreset(preset.value)}
              type="button"
            >
              {preset.label}
              {value.range === preset.value ? (
                <Check className="size-3" />
              ) : null}
            </button>
          ))}
        </div>
        <form
          className="p-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (draftTo <= draftFrom) {
              setError("End time must be later than start time.");
              return;
            }
            onChange({ from: draftFrom, range: "custom", to: draftTo });
            setError("");
            setOpen(false);
          }}
        >
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Exact range
          </p>
          <div className="mt-2 grid gap-2">
            <div className="grid gap-1">
              <p className="text-[8px] text-muted-foreground">From</p>
              <DateTimePicker
                onChange={setDraftFrom}
                onOpenChange={(nextOpen) => {
                  nestedOpen.current = nextOpen;
                }}
                precision="second"
                value={draftFrom}
              />
            </div>
            <div className="grid gap-1">
              <p className="text-[8px] text-muted-foreground">To</p>
              <DateTimePicker
                onChange={setDraftTo}
                onOpenChange={(nextOpen) => {
                  nestedOpen.current = nextOpen;
                }}
                precision="second"
                value={draftTo}
              />
            </div>
          </div>
          {error ? (
            <p aria-live="polite" className="mt-2 text-[9px] text-destructive">
              {error}
            </p>
          ) : null}
          <Button className="mt-3 w-full" size="sm" type="submit">
            Apply exact range
          </Button>
        </form>
      </PopoverContent>
    </Popover>
  );
};
