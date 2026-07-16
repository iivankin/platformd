import { Combobox } from "@base-ui/react/combobox";
import { useRef } from "react";

import {
  insertVariableReference,
  variableReferenceQuery,
  variableSuggestionMatches,
} from "@/service-variable-model";
import type { VariableRow, VariableSuggestion } from "@/service-variable-model";

const valuePlaceholder = `value or ${String.fromCodePoint(36)}{{service.VARIABLE}}`;

const VariableSuggestionPopup = ({
  expressionFirst = false,
}: {
  expressionFirst?: boolean;
}) => (
  <Combobox.Portal>
    <Combobox.Positioner align="start" className="z-50" sideOffset={4}>
      <Combobox.Popup className="max-h-72 w-[var(--anchor-width)] max-w-[calc(100vw-2rem)] min-w-80 overflow-y-auto border border-border bg-popover p-1 text-popover-foreground shadow-lg">
        <Combobox.Empty className="px-3 py-4 text-[10px] text-muted-foreground empty:hidden">
          No matching resource variable.
        </Combobox.Empty>
        <Combobox.List>
          {(suggestion: VariableSuggestion) => (
            <Combobox.Item
              className="grid cursor-default grid-cols-[minmax(0,1fr)_auto] gap-4 px-2.5 py-2 text-[10px] outline-none data-[highlighted]:bg-muted"
              key={`${suggestion.source}:${suggestion.variableName}:${suggestion.expression}`}
              value={suggestion}
            >
              <span className="truncate">
                {expressionFirst
                  ? suggestion.expression
                  : suggestion.variableName}
              </span>
              <span className="text-muted-foreground">
                {expressionFirst ? suggestion.variableName : suggestion.source}
              </span>
            </Combobox.Item>
          )}
        </Combobox.List>
      </Combobox.Popup>
    </Combobox.Positioner>
  </Combobox.Portal>
);

export const VariableNameCombobox = ({
  busy,
  onChange,
  onSelect,
  row,
  suggestions,
}: {
  busy: boolean;
  onChange: (name: string) => void;
  onSelect: (suggestion: VariableSuggestion) => void;
  row: VariableRow;
  suggestions: VariableSuggestion[];
}) => (
  <Combobox.Root<VariableSuggestion>
    autoHighlight
    disabled={busy}
    inputValue={row.name}
    itemToStringLabel={(suggestion) => suggestion.variableName}
    items={suggestions}
    onInputValueChange={(name, eventDetails) => {
      // Base UI synchronizes a single-select input when its popup closes. Only
      // actual typing should edit the persisted variable name.
      if (eventDetails.reason === "input-change") {
        onChange(name);
      }
    }}
    onValueChange={(suggestion) => {
      if (suggestion) {
        onSelect(suggestion);
      }
    }}
    value={null}
  >
    <Combobox.Input
      aria-label={`${row.name || "Variable"} name`}
      autoCapitalize="none"
      autoComplete="off"
      className="h-full min-h-12 w-full bg-transparent px-5 font-mono text-[10px] outline-none placeholder:text-muted-foreground/70"
      placeholder="VARIABLE_NAME"
      spellCheck={false}
    />
    <VariableSuggestionPopup />
  </Combobox.Root>
);

export const VariableValueCombobox = ({
  busy,
  onChange,
  row,
  suggestions,
}: {
  busy: boolean;
  onChange: (value: string) => void;
  row: VariableRow;
  suggestions: VariableSuggestion[];
}) => {
  const inputRef = useRef<HTMLInputElement>(null);
  const selectionRef = useRef({
    end: row.value.length,
    start: row.value.length,
  });

  const rememberSelection = (input: HTMLInputElement) => {
    selectionRef.current = {
      end: input.selectionEnd ?? input.value.length,
      start: input.selectionStart ?? input.value.length,
    };
  };

  return (
    <Combobox.Root<VariableSuggestion>
      autoHighlight
      disabled={busy}
      filter={(suggestion, query) =>
        variableSuggestionMatches(
          suggestion,
          variableReferenceQuery(query, selectionRef.current.start)
        )
      }
      inputValue={row.value}
      itemToStringLabel={(suggestion) => suggestion.expression}
      items={suggestions}
      onInputValueChange={(value, eventDetails) => {
        if (eventDetails.reason !== "input-change") {
          return;
        }
        const { target } = eventDetails.event;
        if (target instanceof HTMLInputElement) {
          rememberSelection(target);
        }
        onChange(value);
      }}
      onValueChange={(suggestion) => {
        if (!suggestion) {
          return;
        }
        const selectionStart =
          inputRef.current?.selectionStart ?? selectionRef.current.start;
        const selectionEnd =
          inputRef.current?.selectionEnd ?? selectionRef.current.end;
        const insertion = insertVariableReference(
          row.value,
          suggestion.expression,
          selectionStart,
          selectionEnd
        );
        selectionRef.current = {
          end: insertion.cursor,
          start: insertion.cursor,
        };
        onChange(insertion.value);
        requestAnimationFrame(() => {
          inputRef.current?.focus();
          inputRef.current?.setSelectionRange(
            insertion.cursor,
            insertion.cursor
          );
        });
      }}
      value={null}
    >
      <Combobox.Input
        aria-label={`${row.name || "Variable"} value`}
        autoCapitalize="none"
        autoComplete="off"
        className="h-full min-h-12 w-full border-0 border-r border-border bg-transparent px-5 font-mono text-[10px] outline-none placeholder:text-muted-foreground/70 focus-visible:ring-0"
        onBlur={(event) => rememberSelection(event.currentTarget)}
        onSelect={(event) => rememberSelection(event.currentTarget)}
        placeholder={valuePlaceholder}
        ref={inputRef}
        spellCheck={false}
      />
      <VariableSuggestionPopup expressionFirst />
    </Combobox.Root>
  );
};
