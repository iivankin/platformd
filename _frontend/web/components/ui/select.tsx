import { Select as SelectPrimitive } from "@base-ui/react/select";
import { CheckIcon, ChevronDownIcon, ChevronUpIcon } from "lucide-react";
import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

const Select = SelectPrimitive.Root;

const SelectValue = ({
  className,
  ...properties
}: SelectPrimitive.Value.Props) => (
  <SelectPrimitive.Value
    className={cn("flex flex-1 text-left", className)}
    data-slot="select-value"
    {...properties}
  />
);

const SelectTrigger = ({
  children,
  className,
  size = "default",
  ...properties
}: SelectPrimitive.Trigger.Props & {
  size?: "default" | "sm";
}) => (
  <SelectPrimitive.Trigger
    className={cn(
      "flex w-fit items-center justify-between gap-1.5 rounded-none border border-input bg-transparent py-2 pr-2 pl-2.5 text-xs whitespace-nowrap transition-colors outline-none select-none focus-visible:border-ring focus-visible:ring-1 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-1 aria-invalid:ring-destructive/20 data-placeholder:text-muted-foreground data-[size=default]:h-8 data-[size=sm]:h-7 data-[size=sm]:rounded-none *:data-[slot=select-value]:line-clamp-1 *:data-[slot=select-value]:flex *:data-[slot=select-value]:items-center *:data-[slot=select-value]:gap-1.5 dark:bg-input/30 dark:hover:bg-input/50 dark:aria-invalid:border-destructive/50 dark:aria-invalid:ring-destructive/40 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
      className
    )}
    data-size={size}
    data-slot="select-trigger"
    {...properties}
  >
    {children}
    <SelectPrimitive.Icon
      render={
        <ChevronDownIcon className="pointer-events-none size-4 text-muted-foreground" />
      }
    />
  </SelectPrimitive.Trigger>
);

const SelectScrollUpButton = ({
  className,
  ...properties
}: ComponentProps<typeof SelectPrimitive.ScrollUpArrow>) => (
  <SelectPrimitive.ScrollUpArrow
    className={cn(
      "top-0 z-10 flex w-full cursor-default items-center justify-center bg-popover py-1 [&_svg:not([class*='size-'])]:size-4",
      className
    )}
    data-slot="select-scroll-up-button"
    {...properties}
  >
    <ChevronUpIcon />
  </SelectPrimitive.ScrollUpArrow>
);

const SelectScrollDownButton = ({
  className,
  ...properties
}: ComponentProps<typeof SelectPrimitive.ScrollDownArrow>) => (
  <SelectPrimitive.ScrollDownArrow
    className={cn(
      "bottom-0 z-10 flex w-full cursor-default items-center justify-center bg-popover py-1 [&_svg:not([class*='size-'])]:size-4",
      className
    )}
    data-slot="select-scroll-down-button"
    {...properties}
  >
    <ChevronDownIcon />
  </SelectPrimitive.ScrollDownArrow>
);

const SelectContent = ({
  align = "center",
  alignItemWithTrigger = false,
  alignOffset = 0,
  children,
  className,
  side = "bottom",
  sideOffset = 4,
  ...properties
}: SelectPrimitive.Popup.Props &
  Pick<
    SelectPrimitive.Positioner.Props,
    "align" | "alignItemWithTrigger" | "alignOffset" | "side" | "sideOffset"
  >) => (
  <SelectPrimitive.Portal>
    <SelectPrimitive.Positioner
      align={align}
      alignItemWithTrigger={alignItemWithTrigger}
      alignOffset={alignOffset}
      className="isolate z-50"
      side={side}
      sideOffset={sideOffset}
    >
      <SelectPrimitive.Popup
        className={cn(
          "relative isolate z-50 max-h-(--available-height) w-(--anchor-width) min-w-36 origin-(--transform-origin) overflow-x-hidden overflow-y-auto rounded-none bg-popover text-popover-foreground shadow-md ring-1 ring-foreground/10 duration-100 data-[align-trigger=true]:animate-none data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className
        )}
        data-align-trigger={alignItemWithTrigger}
        data-slot="select-content"
        {...properties}
      >
        <SelectScrollUpButton />
        <SelectPrimitive.List>{children}</SelectPrimitive.List>
        <SelectScrollDownButton />
      </SelectPrimitive.Popup>
    </SelectPrimitive.Positioner>
  </SelectPrimitive.Portal>
);

const SelectItem = ({
  children,
  className,
  ...properties
}: SelectPrimitive.Item.Props) => (
  <SelectPrimitive.Item
    className={cn(
      "relative flex w-full cursor-default items-center gap-2 rounded-none py-2 pr-8 pl-2 text-xs outline-hidden select-none focus:bg-accent focus:text-accent-foreground not-data-[variant=destructive]:focus:**:text-accent-foreground data-disabled:pointer-events-none data-disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4 *:[span]:last:flex *:[span]:last:items-center *:[span]:last:gap-2",
      className
    )}
    data-slot="select-item"
    {...properties}
  >
    <SelectPrimitive.ItemText className="flex flex-1 shrink-0 gap-2 whitespace-nowrap">
      {children}
    </SelectPrimitive.ItemText>
    <SelectPrimitive.ItemIndicator
      render={
        <span className="pointer-events-none absolute right-2 flex size-4 items-center justify-center" />
      }
    >
      <CheckIcon className="pointer-events-none" />
    </SelectPrimitive.ItemIndicator>
  </SelectPrimitive.Item>
);

export { Select, SelectContent, SelectItem, SelectTrigger, SelectValue };
