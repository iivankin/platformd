import { Combobox } from "@base-ui/react/combobox";
import { Network, RadioTower } from "lucide-react";

import type { ContainerPort } from "@/api";
import { cn } from "@/lib/utils";
import type { ContainerPortDetectionStatus } from "@/use-container-ports";

export const ContainerPortCombobox = ({
  ariaLabel,
  className,
  disabled = false,
  onChange,
  placeholder = "Container port",
  ports,
  protocol,
  status = "ready",
  value,
}: {
  ariaLabel: string;
  className?: string;
  disabled?: boolean;
  onChange: (value: number) => void;
  placeholder?: string;
  ports: ContainerPort[];
  protocol: ContainerPort["protocol"];
  status?: ContainerPortDetectionStatus;
  value: number;
}) => {
  const items = ports
    .filter((item) => item.protocol === protocol)
    .filter(
      (item, index, current) =>
        current.findIndex((candidate) => candidate.port === item.port) === index
    );
  let emptyText = "No matching listening ports. Enter one manually.";
  if (status === "loading") {
    emptyText = "Detecting listening ports…";
  } else if (status === "unavailable") {
    emptyText = "Live ports unavailable. Enter one manually.";
  }

  return (
    <Combobox.Root<ContainerPort>
      autoHighlight
      disabled={disabled}
      filter={(item, query) => String(item.port).includes(query.trim())}
      inputValue={value > 0 ? String(value) : ""}
      itemToStringLabel={(item) => String(item.port)}
      items={items}
      onInputValueChange={(next, details) => {
        if (details.reason === "input-change" && /^\d{0,5}$/u.test(next)) {
          onChange(next === "" ? 0 : Number(next));
        }
      }}
      onValueChange={(item) => item && onChange(item.port)}
      value={null}
    >
      <Combobox.Input
        aria-label={ariaLabel}
        autoComplete="off"
        className={cn(
          "h-8 w-full min-w-0 border border-input bg-background px-2.5 text-xs text-foreground outline-none placeholder:text-muted-foreground/55 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
          className
        )}
        inputMode="numeric"
        placeholder={placeholder}
      />
      <Combobox.Portal>
        <Combobox.Positioner align="start" className="z-50" sideOffset={4}>
          <Combobox.Popup className="max-h-64 w-[var(--anchor-width)] min-w-56 overflow-y-auto border border-border bg-popover p-1 text-popover-foreground shadow-lg">
            <Combobox.Empty className="px-3 py-4 text-[10px] text-muted-foreground empty:hidden">
              {emptyText}
            </Combobox.Empty>
            <Combobox.List>
              {(item: ContainerPort) => (
                <Combobox.Item
                  className="grid cursor-default grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 px-2.5 py-2 text-[10px] outline-none data-[highlighted]:bg-muted"
                  key={`${item.protocol}:${item.port}`}
                  value={item}
                >
                  {item.protocol === "tcp" ? (
                    <Network className="size-3 text-muted-foreground" />
                  ) : (
                    <RadioTower className="size-3 text-muted-foreground" />
                  )}
                  <span className="font-mono">{item.port}</span>
                  <span className="text-[8px] text-muted-foreground uppercase">
                    {item.protocol} · listening
                  </span>
                </Combobox.Item>
              )}
            </Combobox.List>
          </Combobox.Popup>
        </Combobox.Positioner>
      </Combobox.Portal>
    </Combobox.Root>
  );
};
