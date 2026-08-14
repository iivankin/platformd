import { Check, Clock3 } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
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
  { label: "Last 15 minutes", milliseconds: 15 * 60_000, value: "15m" },
  { label: "Last hour", milliseconds: 60 * 60_000, value: "1h" },
  { label: "Last 6 hours", milliseconds: 6 * 60 * 60_000, value: "6h" },
  { label: "Last 24 hours", milliseconds: 24 * 60 * 60_000, value: "24h" },
  { label: "Last 7 days", milliseconds: 7 * 24 * 60 * 60_000, value: "7d" },
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
  const duration = presetMilliseconds[state.range];
  return duration === undefined ? {} : { from: now - duration, to: now };
};

const localDateTime = (value: number) => {
  const date = new Date(value);
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(value - offset).toISOString().slice(0, 19);
};

const parseLocalDateTime = (value: FormDataEntryValue | null) => {
  const timestamp = new Date(String(value ?? "")).getTime();
  return Number.isFinite(timestamp) ? timestamp : undefined;
};

export const formatTelemetryRange = (
  from: number,
  to: number,
  locale?: Intl.LocalesArgument
) => {
  const showSeconds = to - from < 60_000;
  return new Intl.DateTimeFormat(locale, {
    day: "numeric",
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
  const [draftAnchor, setDraftAnchor] = useState(value.to ?? value.from ?? 0);
  const defaultTo = value.to ?? draftAnchor;
  const defaultFrom = value.from ?? defaultTo - 60 * 60_000;

  const choosePreset = (range: Exclude<TelemetryTimeRange, "custom">) => {
    onChange({ from: null, range, to: null });
    setError("");
    setOpen(false);
  };

  return (
    <Popover
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (nextOpen) {
          setDraftAnchor(value.to ?? Date.now());
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
          key={`${value.from ?? ""}:${value.to ?? ""}:${open.toString()}`}
          onSubmit={(event) => {
            event.preventDefault();
            const form = new FormData(event.currentTarget);
            const from = parseLocalDateTime(form.get("from"));
            const to = parseLocalDateTime(form.get("to"));
            if (from === undefined || to === undefined || to <= from) {
              setError("End time must be later than start time.");
              return;
            }
            onChange({ from, range: "custom", to });
            setError("");
            setOpen(false);
          }}
        >
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Exact range
          </p>
          <div className="mt-2 grid grid-cols-2 gap-2">
            <label
              className="text-[8px] text-muted-foreground"
              htmlFor="telemetry-time-from"
            >
              From
              <Input
                className="mt-1 px-2 text-[9px]"
                defaultValue={localDateTime(defaultFrom)}
                id="telemetry-time-from"
                name="from"
                step={1}
                type="datetime-local"
              />
            </label>
            <label
              className="text-[8px] text-muted-foreground"
              htmlFor="telemetry-time-to"
            >
              To
              <Input
                className="mt-1 px-2 text-[9px]"
                defaultValue={localDateTime(defaultTo)}
                id="telemetry-time-to"
                name="to"
                step={1}
                type="datetime-local"
              />
            </label>
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
