import { Popover } from "@base-ui/react/popover";
import { Braces, Copy, Pencil, TextAlignStart, X } from "lucide-react";
import { useState } from "react";
import type { ReactNode } from "react";

import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type {
  PostgresCellEditValue,
  PostgresCellEditorKind,
  PostgresEnumType,
} from "@/postgres-data-browser-model";
import { PostgresDataCellEditor } from "@/postgres-data-cell-editor";

type PostgresCell =
  PostgresQueryResult["statements"][number]["rows"][number][number];

const jsonTypeOIDs = new Set([114, 3802]);

export type PostgresCellDisplay =
  | {
      copyText: string;
      formatted: string;
      kind: "json";
      preview: string;
    }
  | {
      copyText: string;
      kind: "binary";
      preview: string;
      sizeLabel: string;
    }
  | {
      copyText: string;
      formatted: string;
      kind: "text";
      multiline: boolean;
      preview: string;
    }
  | { kind: "null"; preview: "NULL" }
  | { kind: "empty"; preview: "EMPTY" };

const truncatePreview = (value: string, max = 120) =>
  value.length > max ? `${value.slice(0, max)}…` : value;

const tryFormatJSON = (value: string): string | undefined => {
  const trimmed = value.trim();
  if (
    !(
      (trimmed.startsWith("{") && trimmed.endsWith("}")) ||
      (trimmed.startsWith("[") && trimmed.endsWith("]"))
    )
  ) {
    return undefined;
  }
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return undefined;
  }
};

const binarySizeLabel = (base64: string) => {
  let padding = 0;
  if (base64.endsWith("==")) {
    padding = 2;
  } else if (base64.endsWith("=")) {
    padding = 1;
  }
  const bytes = Math.max(0, Math.floor((base64.length * 3) / 4) - padding);
  if (bytes < 1024) {
    return `${bytes.toLocaleString()} B`;
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KB`;
  }
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
};

const cellViewerKindLabel = (
  kind: PostgresCellDisplay["kind"],
  editorKind: PostgresCellEditorKind,
  enumName?: string
) => {
  if (editorKind === "enum" && enumName) {
    return `Enum · ${enumName}`;
  }
  switch (kind) {
    case "json": {
      return "JSON value";
    }
    case "binary": {
      return "Binary value";
    }
    case "null": {
      return "Null value";
    }
    case "empty": {
      return "Empty string";
    }
    default: {
      switch (editorKind) {
        case "boolean": {
          return "Boolean value";
        }
        case "date": {
          return "Date value";
        }
        case "time": {
          return "Time value";
        }
        case "timestamp": {
          return "Timestamp value";
        }
        case "number": {
          return "Numeric value";
        }
        default: {
          return "Field value";
        }
      }
    }
  }
};

export const postgresCellDisplay = (
  cell: PostgresCell | undefined,
  typeOID?: number
): PostgresCellDisplay => {
  if (!cell || cell.null) {
    return { kind: "null", preview: "NULL" };
  }
  if (cell.base64 !== undefined) {
    const preview = truncatePreview(cell.base64, 48);
    return {
      copyText: cell.base64,
      kind: "binary",
      preview,
      sizeLabel: binarySizeLabel(cell.base64),
    };
  }
  const text = cell.text ?? "";
  if (text === "") {
    return { kind: "empty", preview: "EMPTY" };
  }
  const isJSONType = typeOID !== undefined && jsonTypeOIDs.has(typeOID);
  const formattedJSON = isJSONType
    ? (tryFormatJSON(text) ?? text)
    : tryFormatJSON(text);
  if (formattedJSON !== undefined || isJSONType) {
    const formatted = formattedJSON ?? text;
    return {
      copyText: text,
      formatted,
      kind: "json",
      preview: truncatePreview(text.replaceAll(/\s+/gu, " ")),
    };
  }
  return {
    copyText: text,
    formatted: text,
    kind: "text",
    multiline: text.includes("\n") || text.length > 80,
    preview: truncatePreview(text.replaceAll("\n", " ")),
  };
};

const CellPreview = ({ display }: { display: PostgresCellDisplay }) => {
  if (display.kind === "null" || display.kind === "empty") {
    return (
      <span className="text-muted-foreground italic">{display.preview}</span>
    );
  }
  if (display.kind === "binary") {
    return (
      <span className="text-muted-foreground">
        <span className="font-medium tracking-[0.08em] uppercase">BINARY</span>
        <span className="mx-1 text-muted-foreground/50">·</span>
        <span>{display.sizeLabel}</span>
        <span className="mx-1 text-muted-foreground/50">·</span>
        <span className="font-mono">{display.preview}</span>
      </span>
    );
  }
  if (display.kind === "json") {
    return (
      <span className="inline-flex min-w-0 items-center gap-1.5">
        <Braces className="size-3 shrink-0 text-muted-foreground" />
        <span className="truncate font-mono">{display.preview}</span>
      </span>
    );
  }
  return (
    <span className="inline-flex min-w-0 items-center gap-1.5">
      {display.multiline ? (
        <TextAlignStart className="size-3 shrink-0 text-muted-foreground" />
      ) : null}
      <span className="truncate">{display.preview}</span>
    </span>
  );
};

const CellViewerBody = ({ display }: { display: PostgresCellDisplay }) => {
  if (display.kind === "null" || display.kind === "empty") {
    return (
      <p className="px-3 py-3 text-[10px] text-muted-foreground italic">
        {display.preview}
      </p>
    );
  }
  if (display.kind === "binary") {
    return (
      <div className="space-y-2 px-3 py-3">
        <p className="text-[9px] text-muted-foreground">
          Binary value · {display.sizeLabel}
        </p>
        <pre className="max-h-64 overflow-auto border border-border bg-muted/20 p-2 font-mono text-[9px] leading-4 break-all whitespace-pre-wrap">
          {display.copyText}
        </pre>
      </div>
    );
  }
  return (
    <pre
      className={cn(
        "max-h-80 overflow-auto px-3 py-3 font-mono text-[10px] leading-4 whitespace-pre-wrap",
        display.kind === "json" && "bg-muted/10"
      )}
    >
      {display.formatted}
    </pre>
  );
};

const CellPopover = ({
  canEdit,
  canSetNull,
  column,
  display,
  editorKind,
  enumType,
  onSave,
  typeLabel,
}: {
  canEdit: boolean;
  canSetNull: boolean;
  column: string;
  display: PostgresCellDisplay;
  editorKind: PostgresCellEditorKind;
  enumType?: PostgresEnumType;
  onSave?: (value: PostgresCellEditValue) => Promise<void>;
  typeLabel: string;
}) => {
  const [copied, setCopied] = useState(false);
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const muted = display.kind === "null" || display.kind === "empty";
  const copyText =
    display.kind === "null" || display.kind === "empty"
      ? undefined
      : display.copyText;
  const editable = canEdit && onSave !== undefined && editorKind !== "binary";

  const copy = async () => {
    if (copyText === undefined) {
      return;
    }
    await navigator.clipboard.writeText(copyText);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  };

  const save = async (value: PostgresCellEditValue) => {
    if (!onSave) {
      return;
    }
    setBusy(true);
    try {
      await onSave(value);
      setEditing(false);
      setOpen(false);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Popover.Root
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (!nextOpen) {
          setEditing(false);
        }
      }}
      open={open}
    >
      <Popover.Trigger
        className={cn(
          "flex min-w-0 flex-1 items-center px-3 py-2 text-left outline-none hover:bg-muted/30 focus-visible:bg-muted/40 data-[popup-open]:bg-muted/40",
          muted && "text-muted-foreground"
        )}
        onDoubleClick={() => {
          if (editable) {
            setOpen(true);
            setEditing(true);
          }
        }}
        title={editable ? "View or edit value" : "View value"}
      >
        <CellPreview display={display} />
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner
          align="start"
          className="z-50"
          side="bottom"
          sideOffset={4}
        >
          <Popover.Popup className="flex w-[min(28rem,calc(100vw-2rem))] flex-col border border-border bg-popover text-popover-foreground shadow-lg outline-none">
            <header className="flex items-center gap-2 border-b border-border px-3 py-2">
              <div className="min-w-0 flex-1">
                <Popover.Title className="truncate text-[10px] font-medium">
                  {column}
                </Popover.Title>
                <Popover.Description className="truncate text-[8px] text-muted-foreground">
                  {editing
                    ? `Edit · ${typeLabel}`
                    : cellViewerKindLabel(
                        display.kind,
                        editorKind,
                        enumType?.name
                      )}
                </Popover.Description>
              </div>
              {editing || copyText === undefined ? null : (
                <Button
                  aria-label="Copy field value"
                  onClick={() => void copy()}
                  size="icon"
                  title={copied ? "Copied" : "Copy"}
                  variant="ghost"
                >
                  <Copy />
                </Button>
              )}
              {editing || !editable ? null : (
                <Button
                  aria-label="Edit field value"
                  onClick={() => setEditing(true)}
                  size="icon"
                  title="Edit"
                  variant="ghost"
                >
                  <Pencil />
                </Button>
              )}
              <Popover.Close
                aria-label="Close value viewer"
                className="grid size-7 place-items-center text-muted-foreground hover:text-foreground"
              >
                <X className="size-3.5" />
              </Popover.Close>
            </header>
            {editing && onSave ? (
              <PostgresDataCellEditor
                busy={busy}
                canSetNull={canSetNull}
                enumType={enumType}
                isNull={display.kind === "null"}
                kind={editorKind}
                onCancel={() => setEditing(false)}
                onSave={save}
                text={
                  display.kind === "null" || display.kind === "empty"
                    ? undefined
                    : display.copyText
                }
              />
            ) : (
              <CellViewerBody display={display} />
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
};

export const PostgresDataCell = ({
  canEdit = false,
  canSetNull = true,
  cell,
  column,
  editorKind,
  enumType,
  onSave,
  relationTrigger,
  typeLabel,
  typeOID,
}: {
  canEdit?: boolean;
  canSetNull?: boolean;
  cell?: PostgresCell;
  column: string;
  editorKind: PostgresCellEditorKind;
  enumType?: PostgresEnumType;
  onSave?: (value: PostgresCellEditValue) => Promise<void>;
  relationTrigger?: ReactNode;
  typeLabel: string;
  typeOID: number;
}) => {
  const display = postgresCellDisplay(cell, typeOID);
  const muted = display.kind === "null" || display.kind === "empty";
  return (
    <div
      className={cn(
        "group/cell flex h-full min-h-8 items-stretch",
        muted && "bg-muted/10"
      )}
    >
      <CellPopover
        canEdit={canEdit}
        canSetNull={canSetNull}
        column={column}
        display={display}
        editorKind={editorKind}
        enumType={enumType}
        onSave={onSave}
        typeLabel={typeLabel}
      />
      {relationTrigger}
    </div>
  );
};
