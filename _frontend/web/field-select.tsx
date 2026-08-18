import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";

export const FieldSelect = ({
  "aria-label": ariaLabel,
  className,
  disabled,
  id,
  items,
  onValueChange,
  size = "default",
  value,
}: {
  "aria-label"?: string;
  className?: string;
  disabled?: boolean;
  id?: string;
  items: { label: string; value: string }[];
  onValueChange: (value: string) => void;
  size?: "default" | "sm";
  value: string;
}) => (
  <Select
    disabled={disabled}
    items={items}
    onValueChange={(next) => onValueChange(String(next))}
    value={value}
  >
    <SelectTrigger
      aria-label={ariaLabel}
      className={cn("h-8 w-full min-w-0 text-xs", className)}
      disabled={disabled}
      id={id}
      size={size}
    >
      <SelectValue />
    </SelectTrigger>
    <SelectContent align="start">
      {items.map((item) => (
        <SelectItem key={item.value} value={item.value}>
          {item.label}
        </SelectItem>
      ))}
    </SelectContent>
  </Select>
);
