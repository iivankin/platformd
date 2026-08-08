import { format, isValid, parse } from "date-fns";
import { CalendarIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { Calendar } from "@/components/ui/calendar";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import type {
  PostgresCellEditValue,
  PostgresCellEditorKind,
  PostgresEnumType,
} from "@/postgres-data-browser-model";

const fieldClassName =
  "h-8 w-full border border-border bg-background px-2.5 text-[10px] outline-none focus-visible:border-ring focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50";

const emptySelectValue = "__empty__";

const toDateInputValue = (value: string) => {
  const match = /^(?<date>\d{4}-\d{2}-\d{2})/u.exec(value.trim());
  return match?.groups?.date ?? "";
};

const splitTimestampValue = (value: string) => {
  const match = /^(?<date>\d{4}-\d{2}-\d{2})(?:[T ](?<rest>\S.*))?$/u.exec(
    value.trim()
  );
  return {
    date: match?.groups?.date ?? "",
    rest: match?.groups?.rest?.trim() ?? "",
  };
};

const joinTimestampValue = (date: string, rest: string) => {
  if (date === "") {
    return "";
  }
  const timePart = rest.trim() === "" ? "00:00:00" : rest.trim();
  return `${date} ${timePart}`;
};

const parseDateValue = (value: string) => {
  const normalized = toDateInputValue(value);
  if (normalized === "") {
    return;
  }
  const parsed = parse(normalized, "yyyy-MM-dd", new Date());
  return isValid(parsed) ? parsed : undefined;
};

const datePickerLabel = (value: string, allowEmpty: boolean) => {
  if (value !== "") {
    return value;
  }
  return allowEmpty ? "DEFAULT…" : "Pick date…";
};

const initialDraft = ({
  isNull,
  kind,
  text,
}: {
  isNull: boolean;
  kind: PostgresCellEditorKind;
  text?: string;
}) => {
  if (isNull || text === undefined) {
    return "";
  }
  if (kind === "date") {
    return toDateInputValue(text);
  }
  if (kind === "time" || kind === "timestamp") {
    // Keep the full Postgres literal (seconds, fractions, offset) so open→save
    // without edits does not silently truncate the value.
    return text.trim();
  }
  if (kind === "boolean") {
    const normalized = text.trim().toLowerCase();
    if (normalized === "true" || normalized === "t" || normalized === "1") {
      return "true";
    }
    if (normalized === "false" || normalized === "f" || normalized === "0") {
      return "false";
    }
    return "";
  }
  return text;
};

const OptionSelect = ({
  allowEmpty = false,
  busy,
  draft,
  emptyLabel = "DEFAULT",
  items,
  label,
  onChange,
}: {
  allowEmpty?: boolean;
  busy: boolean;
  draft: string;
  emptyLabel?: string;
  items: { label: string; value: string }[];
  label: string;
  onChange: (value: string) => void;
}) => {
  const selectItems = allowEmpty
    ? [{ label: emptyLabel, value: emptySelectValue }, ...items]
    : items;
  const itemsMap = Object.fromEntries(
    selectItems.map((item) => [item.value, item.label])
  );
  const value = draft === "" && allowEmpty ? emptySelectValue : draft;

  return (
    <Select
      disabled={busy}
      items={itemsMap}
      onValueChange={(selected) => {
        const next = String(selected);
        onChange(next === emptySelectValue ? "" : next);
      }}
      value={value === "" ? undefined : value}
    >
      <SelectTrigger aria-label={label} className="h-8 w-full text-[10px]">
        <SelectValue placeholder={allowEmpty ? emptyLabel : "Select…"} />
      </SelectTrigger>
      <SelectContent align="start">
        {selectItems.map((item) => (
          <SelectItem key={item.value} value={item.value}>
            {item.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
};

const DatePicker = ({
  allowEmpty = false,
  busy,
  onChange,
  value,
}: {
  allowEmpty?: boolean;
  busy: boolean;
  onChange: (value: string) => void;
  value: string;
}) => {
  const [open, setOpen] = useState(false);
  const selected = parseDateValue(value);

  return (
    <div className="flex gap-1.5">
      <Popover onOpenChange={setOpen} open={open}>
        <PopoverTrigger
          disabled={busy}
          render={
            <Button
              aria-label="Pick date"
              className={cn(
                "h-8 min-w-0 flex-1 justify-start gap-2 px-2.5 text-[10px] font-normal",
                value === "" && "text-muted-foreground"
              )}
              disabled={busy}
              variant="outline"
            >
              <CalendarIcon className="size-3.5 text-muted-foreground" />
              <span className="truncate">
                {datePickerLabel(value, allowEmpty)}
              </span>
            </Button>
          }
        />
        <PopoverContent align="start" className="w-auto p-0">
          <Calendar
            mode="single"
            onSelect={(date) => {
              if (!date) {
                return;
              }
              onChange(format(date, "yyyy-MM-dd"));
              setOpen(false);
            }}
            selected={selected}
          />
        </PopoverContent>
      </Popover>
      {allowEmpty && value !== "" ? (
        <Button
          aria-label="Clear date"
          className="h-8 shrink-0 px-2 text-[10px]"
          disabled={busy}
          onClick={() => onChange("")}
          size="sm"
          variant="ghost"
        >
          Clear
        </Button>
      ) : null}
    </div>
  );
};

const TimestampEditor = ({
  allowEmpty = false,
  busy,
  draft,
  onChange,
}: {
  allowEmpty?: boolean;
  busy: boolean;
  draft: string;
  onChange: (value: string) => void;
}) => {
  const { date, rest } = splitTimestampValue(draft);

  return (
    <div className="flex flex-col gap-1.5">
      <DatePicker
        allowEmpty={allowEmpty}
        busy={busy}
        onChange={(nextDate) => {
          onChange(joinTimestampValue(nextDate, rest));
        }}
        value={date}
      />
      <Input
        aria-label="Time value"
        className="h-8 font-mono text-[10px]"
        disabled={busy || date === ""}
        onChange={(event) => {
          onChange(joinTimestampValue(date, event.target.value));
        }}
        placeholder="HH:MM:SS[.frac][+tz]"
        spellCheck={false}
        value={rest}
      />
    </div>
  );
};

const ValueEditor = ({
  allowEmpty = false,
  busy,
  draft,
  enumType,
  kind,
  onChange,
  onSubmit,
}: {
  allowEmpty?: boolean;
  busy: boolean;
  draft: string;
  enumType?: PostgresEnumType;
  kind: PostgresCellEditorKind;
  onChange: (value: string) => void;
  onSubmit?: () => void;
}) => {
  if (kind === "boolean") {
    return (
      <OptionSelect
        allowEmpty={allowEmpty}
        busy={busy}
        draft={draft}
        items={[
          { label: "true", value: "true" },
          { label: "false", value: "false" },
        ]}
        label="Boolean value"
        onChange={onChange}
      />
    );
  }
  if (kind === "enum") {
    return (
      <OptionSelect
        allowEmpty={allowEmpty}
        busy={busy}
        draft={draft}
        items={(enumType?.labels ?? []).map((label) => ({
          label,
          value: label,
        }))}
        label="Enum value"
        onChange={onChange}
      />
    );
  }
  if (kind === "date") {
    return (
      <DatePicker
        allowEmpty={allowEmpty}
        busy={busy}
        onChange={onChange}
        value={draft}
      />
    );
  }
  if (kind === "timestamp") {
    return (
      <TimestampEditor
        allowEmpty={allowEmpty}
        busy={busy}
        draft={draft}
        onChange={onChange}
      />
    );
  }
  if (kind === "json") {
    return (
      <textarea
        aria-label="JSON value"
        className={cn(
          fieldClassName,
          "min-h-36 resize-y py-2 font-mono leading-4"
        )}
        disabled={busy}
        onChange={(event) => onChange(event.target.value)}
        spellCheck={false}
        value={draft}
      />
    );
  }
  return (
    <Input
      aria-label="Cell value"
      className={cn(
        "h-8 text-[10px]",
        (kind === "number" || kind === "text" || kind === "time") && "font-mono"
      )}
      disabled={busy}
      inputMode={kind === "number" ? "decimal" : undefined}
      onChange={(event) => onChange(event.target.value)}
      onKeyDown={(event) => {
        if (event.key === "Enter" && !event.shiftKey && onSubmit) {
          event.preventDefault();
          onSubmit();
        }
      }}
      placeholder={allowEmpty ? "DEFAULT" : undefined}
      spellCheck={false}
      type="text"
      value={draft}
    />
  );
};

export const serializePostgresFieldDraft = (
  _kind: PostgresCellEditorKind,
  draft: string
): PostgresCellEditValue => ({
  kind: "text",
  text: draft,
});

export const PostgresDataFieldControl = ({
  allowEmpty = false,
  busy = false,
  draft,
  enumType,
  kind,
  onChange,
  onSubmit,
}: {
  allowEmpty?: boolean;
  busy?: boolean;
  draft: string;
  enumType?: PostgresEnumType;
  kind: PostgresCellEditorKind;
  onChange: (value: string) => void;
  onSubmit?: () => void;
}) => (
  <ValueEditor
    allowEmpty={allowEmpty}
    busy={busy}
    draft={draft}
    enumType={enumType}
    kind={kind}
    onChange={onChange}
    onSubmit={onSubmit}
  />
);

export const PostgresDataCellEditor = ({
  busy,
  canSetNull = true,
  enumType,
  error,
  isNull,
  kind,
  onCancel,
  onSave,
  text,
}: {
  busy: boolean;
  canSetNull?: boolean;
  enumType?: PostgresEnumType;
  error?: string;
  isNull: boolean;
  kind: PostgresCellEditorKind;
  onCancel: () => void;
  onSave: (value: PostgresCellEditValue) => Promise<void>;
  text?: string;
}) => {
  const [draft, setDraft] = useState(() =>
    initialDraft({ isNull, kind, text })
  );
  const [setNull, setSetNull] = useState(isNull && canSetNull);
  const [localError, setLocalError] = useState<string>();

  if (kind === "binary") {
    return (
      <div className="space-y-2 px-3 py-3">
        <p className="text-[9px] text-muted-foreground">
          Binary values cannot be edited here. Use the SQL console.
        </p>
        <Button disabled={busy} onClick={onCancel} size="sm" variant="ghost">
          Close
        </Button>
      </div>
    );
  }

  const save = async (value: PostgresCellEditValue) => {
    setLocalError(undefined);
    try {
      await onSave(value);
    } catch (saveError) {
      setLocalError(
        saveError instanceof Error ? saveError.message : "Unable to save value"
      );
    }
  };

  const submit = () => {
    if (setNull) {
      if (!canSetNull) {
        setLocalError("Column does not allow NULL");
        return;
      }
      void save({ kind: "null" });
      return;
    }
    if (kind === "json" && draft.trim() !== "") {
      try {
        JSON.parse(draft);
      } catch {
        setLocalError("JSON value is invalid");
        return;
      }
    }
    if (
      (kind === "date" ||
        kind === "time" ||
        kind === "timestamp" ||
        kind === "boolean" ||
        kind === "enum") &&
      draft.trim() === ""
    ) {
      setLocalError("Value is required, or set NULL");
      return;
    }
    void save({ kind: "text", text: draft });
  };

  return (
    <div className="space-y-2 px-3 py-3">
      {setNull ? (
        <p className="border border-border bg-muted/20 px-2.5 py-2 text-[10px] text-muted-foreground italic">
          NULL
        </p>
      ) : (
        <ValueEditor
          busy={busy}
          draft={draft}
          enumType={enumType}
          kind={kind}
          onChange={setDraft}
          onSubmit={submit}
        />
      )}

      {canSetNull ? (
        <label className="flex items-center gap-2 text-[9px] text-muted-foreground">
          <input
            checked={setNull}
            className="size-3.5 accent-foreground"
            disabled={busy}
            onChange={(event) => setSetNull(event.target.checked)}
            type="checkbox"
          />
          Set NULL
        </label>
      ) : (
        <p className="text-[8px] text-muted-foreground">NOT NULL</p>
      )}

      {(localError ?? error) ? (
        <p aria-live="polite" className="text-[9px] text-destructive">
          {localError ?? error}
        </p>
      ) : null}

      <div className="flex items-center justify-end gap-1.5">
        <Button disabled={busy} onClick={onCancel} size="sm" variant="ghost">
          Cancel
        </Button>
        <Button disabled={busy} onClick={submit} size="sm" variant="outline">
          {busy ? "Saving…" : "Save"}
        </Button>
      </div>
    </div>
  );
};
