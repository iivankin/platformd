import { CalendarIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

const hours = Array.from({ length: 24 }, (_, hour) => hour);
const minutes = Array.from({ length: 60 }, (_, minute) => minute);

const pad2 = (value: number) => String(value).padStart(2, "0");

const TimeColumn = ({
  "aria-label": ariaLabel,
  onChange,
  value,
  values,
}: {
  "aria-label": string;
  onChange: (value: number) => void;
  value: number;
  values: number[];
}) => {
  const selectedRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    selectedRef.current?.parentElement?.scrollTo({
      top: Math.max(
        0,
        (selectedRef.current?.offsetTop ?? 0) -
          (selectedRef.current?.parentElement?.clientHeight ?? 0) / 2 +
          (selectedRef.current?.clientHeight ?? 0) / 2
      ),
    });
  }, []);

  return (
    <div
      aria-label={ariaLabel}
      className="h-64 w-11 overflow-y-auto border-r border-border last:border-r-0"
    >
      {values.map((entry) => (
        <button
          aria-pressed={entry === value}
          className={cn(
            "flex h-7 w-full items-center justify-center text-[10px] hover:bg-muted",
            entry === value && "bg-muted text-foreground"
          )}
          key={entry}
          onClick={() => onChange(entry)}
          ref={entry === value ? selectedRef : undefined}
          type="button"
        >
          {pad2(entry)}
        </button>
      ))}
    </div>
  );
};

const applyParts = (
  value: number,
  parts: { date?: Date; hour?: number; minute?: number; second?: number }
) => {
  const next = new Date(Number.isFinite(value) ? value : Date.now());
  if (parts.date) {
    next.setFullYear(
      parts.date.getFullYear(),
      parts.date.getMonth(),
      parts.date.getDate()
    );
  }
  if (parts.hour !== undefined) {
    next.setHours(parts.hour);
  }
  if (parts.minute !== undefined) {
    next.setMinutes(parts.minute);
  }
  if (parts.second !== undefined) {
    next.setSeconds(parts.second);
  }
  next.setMilliseconds(0);
  return next.getTime();
};

const dateTimeLabel = (value: number, precision: "minute" | "second") =>
  new Intl.DateTimeFormat(undefined, {
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    month: "short",
    second: precision === "second" ? "2-digit" : undefined,
    year: "numeric",
  }).format(new Date(value));

export const DateTimePicker = ({
  className,
  id,
  onChange,
  onOpenChange,
  precision = "minute",
  value,
}: {
  className?: string;
  id?: string;
  onChange: (value: number) => void;
  onOpenChange?: (open: boolean) => void;
  precision?: "minute" | "second";
  value: number;
}) => {
  const [open, setOpen] = useState(false);
  const date = new Date(value);
  const secondValues = precision === "second" ? minutes : undefined;

  return (
    <Popover
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        onOpenChange?.(nextOpen);
      }}
      open={open}
    >
      <PopoverTrigger
        render={
          <Button
            aria-label="Pick date and time"
            className={cn(
              "h-8 w-full min-w-0 justify-start px-2.5 text-[10px] font-normal",
              className
            )}
            id={id}
            type="button"
            variant="outline"
          >
            <CalendarIcon className="size-3.5 text-muted-foreground" />
            <span className="truncate">{dateTimeLabel(value, precision)}</span>
          </Button>
        }
      />
      <PopoverContent align="start" className="w-auto gap-0 p-0">
        <div className="flex">
          <Calendar
            className="[--cell-radius:0px]"
            mode="single"
            onSelect={(selected) => {
              if (!selected) {
                return;
              }
              onChange(applyParts(value, { date: selected }));
            }}
            selected={
              new Date(date.getFullYear(), date.getMonth(), date.getDate())
            }
          />
          <div className="flex border-l border-border">
            <TimeColumn
              aria-label="Hour"
              onChange={(hour) => onChange(applyParts(value, { hour }))}
              value={date.getHours()}
              values={hours}
            />
            <TimeColumn
              aria-label="Minute"
              onChange={(minute) => onChange(applyParts(value, { minute }))}
              value={date.getMinutes()}
              values={minutes}
            />
            {secondValues ? (
              <TimeColumn
                aria-label="Second"
                onChange={(second) => onChange(applyParts(value, { second }))}
                value={date.getSeconds()}
                values={secondValues}
              />
            ) : null}
          </div>
        </div>
      </PopoverContent>
    </Popover>
  );
};
