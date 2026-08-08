import { Popover as PopoverPrimitive } from "@base-ui/react/popover";

import { cn } from "@/lib/utils";

const Popover = (properties: PopoverPrimitive.Root.Props) => (
  <PopoverPrimitive.Root data-slot="popover" {...properties} />
);

const PopoverTrigger = (properties: PopoverPrimitive.Trigger.Props) => (
  <PopoverPrimitive.Trigger data-slot="popover-trigger" {...properties} />
);

const PopoverContent = ({
  align = "center",
  alignOffset = 0,
  className,
  side = "bottom",
  sideOffset = 4,
  ...properties
}: PopoverPrimitive.Popup.Props &
  Pick<
    PopoverPrimitive.Positioner.Props,
    "align" | "alignOffset" | "side" | "sideOffset"
  >) => (
  <PopoverPrimitive.Portal>
    <PopoverPrimitive.Positioner
      align={align}
      alignOffset={alignOffset}
      className="isolate z-50"
      side={side}
      sideOffset={sideOffset}
    >
      <PopoverPrimitive.Popup
        className={cn(
          "z-50 flex w-72 origin-(--transform-origin) flex-col gap-2.5 rounded-none bg-popover p-2.5 text-xs text-popover-foreground shadow-md ring-1 ring-foreground/10 outline-hidden duration-100 data-[side=bottom]:slide-in-from-top-2 data-[side=inline-end]:slide-in-from-left-2 data-[side=inline-start]:slide-in-from-right-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className
        )}
        data-slot="popover-content"
        {...properties}
      />
    </PopoverPrimitive.Positioner>
  </PopoverPrimitive.Portal>
);

export { Popover, PopoverContent, PopoverTrigger };
