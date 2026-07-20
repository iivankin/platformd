import { Combobox } from "@base-ui/react/combobox";
import { Box, Database, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchManagedImageTags } from "@/api";
import type { ManagedImageEngine, ManagedImageTag } from "@/api";
import { cn } from "@/lib/utils";

const engineName = (engine: ManagedImageEngine) =>
  engine === "postgres" ? "PostgreSQL" : "Redis";

export const ManagedImageTagCombobox = ({
  ariaLabel,
  className,
  engine,
  id,
  onChange,
  placeholder,
  required,
  value,
}: {
  ariaLabel?: string;
  className?: string;
  engine: ManagedImageEngine;
  id?: string;
  onChange: (value: string) => void;
  placeholder?: string;
  required?: boolean;
  value: string;
}) => {
  const [failed, setFailed] = useState(false);
  const [items, setItems] = useState<ManagedImageTag[]>([]);
  const [loading, setLoading] = useState(false);
  const query = value.trim();
  const name = engineName(engine);
  const Icon = engine === "postgres" ? Database : Box;
  let emptyText = "No matching official tags. You can enter one manually.";
  if (loading) {
    emptyText = "Loading official tags…";
  } else if (failed) {
    emptyText = "Tags unavailable. You can enter one manually.";
  }

  useEffect(() => {
    const controller = new AbortController();
    const timer = window.setTimeout(
      async () => {
        setLoading(true);
        setFailed(false);
        try {
          const page = await fetchManagedImageTags(
            engine,
            { pageSize: 50, search: query || undefined },
            controller.signal
          );
          setItems(page.tags);
        } catch (error) {
          if (!(error instanceof DOMException && error.name === "AbortError")) {
            setFailed(true);
            setItems([]);
          }
        } finally {
          if (!controller.signal.aborted) {
            setLoading(false);
          }
        }
      },
      query ? 150 : 0
    );

    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [engine, query]);

  return (
    <Combobox.Root<ManagedImageTag>
      autoHighlight
      filter={() => true}
      inputValue={value}
      itemToStringLabel={(item) => item.name}
      items={items}
      onInputValueChange={(next, details) => {
        if (details.reason === "input-change") {
          onChange(next);
        }
      }}
      onValueChange={(item) => item && onChange(item.name)}
      value={null}
    >
      <Combobox.Input
        aria-label={ariaLabel ?? `Official ${name} image tag`}
        autoCapitalize="none"
        autoComplete="off"
        className={cn(
          "h-8 w-full border border-input bg-background px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/55 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring",
          className
        )}
        id={id}
        placeholder={placeholder ?? (engine === "postgres" ? "18.3" : "8.2")}
        required={required}
        spellCheck={false}
      />
      <Combobox.Portal>
        <Combobox.Positioner align="start" className="z-50" sideOffset={4}>
          <Combobox.Popup className="max-h-64 w-[var(--anchor-width)] min-w-72 overflow-y-auto border border-border bg-popover p-1 text-popover-foreground shadow-lg">
            <Combobox.Empty className="px-3 py-4 text-[10px] text-muted-foreground empty:hidden">
              {emptyText}
            </Combobox.Empty>
            <Combobox.List>
              {(item: ManagedImageTag) => (
                <Combobox.Item
                  className="grid cursor-default grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 px-2.5 py-2 text-[10px] outline-none data-[highlighted]:bg-muted"
                  key={item.name}
                  value={item}
                >
                  <Icon className="size-3 text-muted-foreground" />
                  <span className="truncate font-mono">{item.name}</span>
                  <span className="text-[8px] text-muted-foreground uppercase">
                    Official
                  </span>
                </Combobox.Item>
              )}
            </Combobox.List>
            {loading && items.length > 0 ? (
              <div className="flex items-center gap-2 border-t border-border px-2.5 py-2 text-[8px] text-muted-foreground">
                <LoaderCircle className="size-3 animate-spin" /> Updating tags
              </div>
            ) : null}
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  );
};
