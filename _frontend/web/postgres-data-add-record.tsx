import { Dialog } from "@base-ui/react/dialog";
import { Plus, X } from "lucide-react";
import { useState } from "react";

import type { PostgresQueryResult } from "@/api";
import { Button } from "@/components/ui/button";
import {
  postgresCellEditorKind,
  postgresColumnIsRequired,
  postgresTypeLabel,
} from "@/postgres-data-browser-model";
import type {
  PostgresBrowserTable,
  PostgresCellEditValue,
  PostgresCellEditorKind,
  PostgresColumnMeta,
  PostgresEnumType,
} from "@/postgres-data-browser-model";
import {
  PostgresDataFieldControl,
  serializePostgresFieldDraft,
} from "@/postgres-data-cell-editor";

type PostgresColumn =
  PostgresQueryResult["statements"][number]["columns"][number];

interface FieldDraft {
  draft: string;
  setNull: boolean;
}

export type PostgresInsertRowHandler = (input: {
  columns: {
    column: string;
    kind: PostgresCellEditorKind;
    value: PostgresCellEditValue;
  }[];
  table: PostgresBrowserTable;
}) => Promise<void>;

const emptyDraft = (): FieldDraft => ({ draft: "", setNull: false });

const columnMetaByName = (
  columnMeta: readonly PostgresColumnMeta[],
  name: string
) => columnMeta.find((column) => column.name === name);

const fieldConstraintHint = (meta?: PostgresColumnMeta) => {
  if (!meta) {
    return "unknown nullability";
  }
  if (postgresColumnIsRequired(meta)) {
    return "required";
  }
  if (meta.hasDefault && meta.nullable) {
    return "optional · DEFAULT or NULL";
  }
  if (meta.hasDefault) {
    return "optional · DEFAULT";
  }
  return "optional · NULL";
};

export const PostgresDataAddRecordDialog = ({
  columnMeta,
  columns,
  enums,
  onInsert,
  open,
  onOpenChange,
  table,
}: {
  columnMeta: readonly PostgresColumnMeta[];
  columns: PostgresColumn[];
  enums: ReadonlyMap<number, PostgresEnumType>;
  onInsert: PostgresInsertRowHandler;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  table: PostgresBrowserTable;
}) => {
  const [drafts, setDrafts] = useState<Record<string, FieldDraft>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  const reset = () => {
    setDrafts({});
    setError(undefined);
    setBusy(false);
  };

  const fieldState = (column: string) => drafts[column] ?? emptyDraft();

  const setField = (column: string, next: Partial<FieldDraft>) => {
    setDrafts((current) => ({
      ...current,
      [column]: { ...(current[column] ?? emptyDraft()), ...next },
    }));
  };

  const submit = async () => {
    const values: {
      column: string;
      kind: PostgresCellEditorKind;
      value: PostgresCellEditValue;
    }[] = [];
    for (const column of columns) {
      const kind = postgresCellEditorKind(column.typeOid, enums);
      const meta = columnMetaByName(columnMeta, column.name);
      const required = meta ? postgresColumnIsRequired(meta) : false;
      const nullable = meta?.nullable ?? true;
      const hasDefault = meta?.hasDefault ?? true;
      if (kind === "binary") {
        if (required) {
          setError(
            `${column.name}: binary required columns cannot be set here`
          );
          return;
        }
        continue;
      }
      const state = fieldState(column.name);
      if (state.setNull) {
        if (!nullable) {
          setError(`${column.name}: column does not allow NULL`);
          return;
        }
        values.push({ column: column.name, kind, value: { kind: "null" } });
        continue;
      }
      const empty =
        kind === "text" ? state.draft === "" : state.draft.trim() === "";
      if (empty) {
        if (required) {
          setError(`${column.name}: value is required`);
          return;
        }
        // Nullable without DEFAULT still omits the column → Postgres inserts NULL.
        // NOT NULL with DEFAULT omits → DEFAULT. Required already rejected above.
        if (hasDefault || nullable) {
          continue;
        }
        setError(`${column.name}: value is required`);
        return;
      }
      if (kind === "json") {
        try {
          JSON.parse(state.draft);
        } catch {
          setError(`${column.name}: JSON value is invalid`);
          return;
        }
      }
      values.push({
        column: column.name,
        kind,
        value: serializePostgresFieldDraft(kind, state.draft),
      });
    }
    setBusy(true);
    setError(undefined);
    try {
      await onInsert({ columns: values, table });
      reset();
      onOpenChange(false);
    } catch (insertError) {
      setError(
        insertError instanceof Error
          ? insertError.message
          : "Unable to insert row"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        if (busy) {
          return;
        }
        onOpenChange(nextOpen);
        if (!nextOpen) {
          reset();
        }
      }}
      open={open}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px]" />
        <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
          <Dialog.Popup className="flex max-h-[min(40rem,calc(100vh-2rem))] w-full max-w-lg flex-col border border-border bg-background text-foreground shadow-2xl">
            <header className="flex shrink-0 items-start justify-between gap-4 border-b border-border px-4 py-3">
              <div className="min-w-0">
                <Dialog.Title className="text-sm font-medium">
                  Add record
                </Dialog.Title>
                <Dialog.Description className="mt-1 truncate text-[10px] text-muted-foreground">
                  {table.schema}.{table.name} · required fields marked *
                </Dialog.Description>
              </div>
              <Dialog.Close
                aria-label="Close"
                className="grid size-8 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground"
                disabled={busy}
              >
                <X className="size-4" />
              </Dialog.Close>
            </header>

            <div className="min-h-0 flex-1 space-y-3 overflow-auto px-4 py-3">
              {columns.length === 0 ? (
                <p className="text-[10px] text-muted-foreground">
                  Load table columns before adding a record.
                </p>
              ) : null}
              {columns.map((column) => {
                const kind = postgresCellEditorKind(column.typeOid, enums);
                const meta = columnMetaByName(columnMeta, column.name);
                const required = meta ? postgresColumnIsRequired(meta) : false;
                const nullable = meta?.nullable ?? true;
                const allowEmpty = !required;
                const state = fieldState(column.name);
                if (kind === "binary") {
                  return (
                    <div className="space-y-1.5" key={column.name}>
                      <div className="flex items-baseline justify-between gap-2">
                        <span className="text-[10px] font-medium">
                          {column.name}
                          {required ? " *" : ""}
                        </span>
                        <span className="text-[8px] text-muted-foreground">
                          {postgresTypeLabel(column.typeOid, enums)} ·{" "}
                          {fieldConstraintHint(meta)}
                        </span>
                      </div>
                      <p className="border border-border bg-muted/20 px-2.5 py-2 text-[9px] text-muted-foreground">
                        {required
                          ? "Binary required columns cannot be set here"
                          : "Binary columns use DEFAULT"}
                      </p>
                    </div>
                  );
                }
                return (
                  <div className="space-y-1.5" key={column.name}>
                    <div className="flex items-baseline justify-between gap-2">
                      <span className="text-[10px] font-medium">
                        {column.name}
                        {required ? " *" : ""}
                      </span>
                      <span className="text-[8px] text-muted-foreground">
                        {postgresTypeLabel(column.typeOid, enums)} ·{" "}
                        {fieldConstraintHint(meta)}
                      </span>
                    </div>
                    {state.setNull ? (
                      <p className="border border-border bg-muted/20 px-2.5 py-2 text-[10px] text-muted-foreground italic">
                        NULL
                      </p>
                    ) : (
                      <PostgresDataFieldControl
                        allowEmpty={allowEmpty}
                        busy={busy}
                        draft={state.draft}
                        enumType={enums.get(column.typeOid)}
                        kind={kind}
                        onChange={(draft) =>
                          setField(column.name, { draft, setNull: false })
                        }
                      />
                    )}
                    {nullable ? (
                      <label className="flex items-center gap-2 text-[9px] text-muted-foreground">
                        <input
                          checked={state.setNull}
                          className="size-3.5 accent-foreground"
                          disabled={busy}
                          onChange={(event) =>
                            setField(column.name, {
                              setNull: event.target.checked,
                            })
                          }
                          type="checkbox"
                        />
                        Set NULL
                      </label>
                    ) : (
                      <p className="text-[8px] text-muted-foreground">
                        {required
                          ? "Required · no DEFAULT"
                          : "NOT NULL · empty uses DEFAULT"}
                      </p>
                    )}
                  </div>
                );
              })}
            </div>

            {error ? (
              <p className="border-t border-border px-4 py-2 text-[10px] text-destructive">
                {error}
              </p>
            ) : null}

            <footer className="flex shrink-0 justify-end gap-2 border-t border-border px-4 py-3">
              <Dialog.Close
                className="inline-flex h-8 items-center justify-center border border-border px-2.5 text-xs font-medium outline-none hover:bg-muted disabled:opacity-50"
                disabled={busy}
              >
                Cancel
              </Dialog.Close>
              <Button
                disabled={busy || columns.length === 0}
                onClick={() => void submit()}
                size="sm"
                variant="outline"
              >
                <Plus />
                {busy ? "Adding…" : "Add record"}
              </Button>
            </footer>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
};
