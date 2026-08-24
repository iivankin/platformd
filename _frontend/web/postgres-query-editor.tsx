import type { KeyboardEvent } from "react";
import { useId, useMemo, useRef, useState } from "react";

import { postgresQueryCompletion } from "@/postgres-query-suggestions";
import type {
  PostgresQueryCatalogColumn,
  PostgresQuerySuggestion,
} from "@/postgres-query-suggestions";

const suggestionWidth = 288;
const suggestionHeight = 156;

const completionPosition = (
  textarea: HTMLTextAreaElement,
  sql: string,
  cursor: number
) => {
  const lines = sql.slice(0, cursor).split("\n");
  const line = lines.length - 1;
  const column = lines.at(-1)?.length ?? 0;
  return {
    left: Math.min(
      Math.max(8, 16 + column * 6.6 - textarea.scrollLeft),
      Math.max(8, textarea.clientWidth - suggestionWidth - 8)
    ),
    top: Math.min(
      Math.max(8, 16 + (line + 1) * 20 - textarea.scrollTop),
      Math.max(8, textarea.clientHeight - suggestionHeight - 8)
    ),
  };
};

const SuggestionList = ({
  items,
  listboxID,
  onSelect,
  position,
  selectedIndex,
}: {
  items: PostgresQuerySuggestion[];
  listboxID: string;
  onSelect: (item: PostgresQuerySuggestion) => void;
  position: { left: number; top: number };
  selectedIndex: number;
}) => (
  <ul
    aria-label="SQL suggestions"
    className="absolute z-20 max-h-36 w-72 overflow-y-auto border border-border bg-popover text-popover-foreground shadow-[0_10px_30px_rgba(0,0,0,0.28)]"
    id={listboxID}
    style={position}
  >
    {items.map((item, index) => (
      <li key={item.key}>
        <button
          aria-current={index === selectedIndex}
          className="grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-3 border-b border-border px-3 py-2 text-left last:border-b-0 aria-current:bg-muted"
          onMouseDown={(event) => {
            event.preventDefault();
            onSelect(item);
          }}
          tabIndex={-1}
          type="button"
        >
          <span className="truncate font-mono text-[10px]">{item.label}</span>
          <span className="max-w-32 truncate text-[8px] tracking-[0.08em] text-muted-foreground uppercase">
            {item.kind === "keyword" ? item.kind : item.detail}
          </span>
        </button>
      </li>
    ))}
    <li className="sticky bottom-0 border-t border-border bg-popover px-3 py-1.5 text-[8px] text-muted-foreground">
      ↑↓ navigate · Tab or Enter accept · Esc close
    </li>
  </ul>
);

export const PostgresQueryEditor = ({
  catalog,
  onChange,
  onRun,
  sql,
}: {
  catalog: PostgresQueryCatalogColumn[];
  onChange: (sql: string) => void;
  onRun: () => void;
  sql: string;
}) => {
  const listboxID = useId();
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [cursor, setCursor] = useState(0);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const [explicit, setExplicit] = useState(false);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [position, setPosition] = useState({ left: 16, top: 36 });
  const completion = useMemo(
    () => postgresQueryCompletion(sql, cursor, catalog, explicit),
    [catalog, cursor, explicit, sql]
  );
  const visible = focused && !dismissed && completion.items.length > 0;
  const activeIndex = Math.min(
    selectedIndex,
    Math.max(0, completion.items.length - 1)
  );

  const updateCursor = (textarea: HTMLTextAreaElement) => {
    const nextCursor = textarea.selectionStart;
    setCursor(nextCursor);
    setPosition(completionPosition(textarea, textarea.value, nextCursor));
  };

  const accept = (item: PostgresQuerySuggestion) => {
    const nextSQL =
      sql.slice(0, completion.from) +
      item.insertText +
      sql.slice(completion.to);
    const nextCursor = completion.from + item.insertText.length;
    onChange(nextSQL);
    setCursor(nextCursor);
    setDismissed(true);
    setExplicit(false);
    requestAnimationFrame(() => {
      const textarea = textareaRef.current;
      if (!textarea) {
        return;
      }
      textarea.focus();
      textarea.setSelectionRange(nextCursor, nextCursor);
      setPosition(completionPosition(textarea, nextSQL, nextCursor));
    });
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      onRun();
      return;
    }
    if (event.code === "Space" && (event.metaKey || event.ctrlKey)) {
      event.preventDefault();
      setExplicit(true);
      setDismissed(false);
      setSelectedIndex(0);
      updateCursor(event.currentTarget);
      return;
    }
    if (!visible) {
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const direction = event.key === "ArrowDown" ? 1 : -1;
      setSelectedIndex(
        (activeIndex + direction + completion.items.length) %
          completion.items.length
      );
      return;
    }
    if (event.key === "Escape") {
      event.preventDefault();
      setDismissed(true);
      setExplicit(false);
      return;
    }
    if (event.key === "Tab" || event.key === "Enter") {
      const item = completion.items[activeIndex];
      if (item) {
        event.preventDefault();
        accept(item);
      }
    }
  };

  return (
    <div className="relative h-56 shrink-0 border-b border-border">
      <textarea
        aria-autocomplete="list"
        aria-controls={visible ? listboxID : undefined}
        aria-label="PostgreSQL SQL editor"
        className="h-full w-full resize-none bg-background p-4 font-mono text-[11px] leading-5 outline-none focus:bg-muted/10"
        onBlur={() => setFocused(false)}
        onChange={(event) => {
          onChange(event.target.value);
          setDismissed(false);
          setExplicit(false);
          setSelectedIndex(0);
          updateCursor(event.currentTarget);
        }}
        onFocus={(event) => {
          setFocused(true);
          setDismissed(false);
          updateCursor(event.currentTarget);
        }}
        onKeyDown={handleKeyDown}
        onScroll={(event) => updateCursor(event.currentTarget)}
        onSelect={(event) => {
          setExplicit(false);
          setSelectedIndex(0);
          updateCursor(event.currentTarget);
        }}
        ref={textareaRef}
        spellCheck={false}
        value={sql}
        wrap="off"
      />
      {visible ? (
        <SuggestionList
          items={completion.items}
          listboxID={listboxID}
          onSelect={accept}
          position={position}
          selectedIndex={activeIndex}
        />
      ) : null}
      <span aria-live="polite" className="sr-only">
        {visible
          ? `${completion.items.length} suggestions. ${completion.items[activeIndex]?.label ?? ""} selected.`
          : ""}
      </span>
    </div>
  );
};
