import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { RedisInputEncoding } from "@/redis-data-utils";

interface RedisBinaryInputProperties {
  encoding: RedisInputEncoding;
  label: string;
  multiline?: boolean;
  onEncodingChange: (encoding: RedisInputEncoding) => void;
  onValueChange: (value: string) => void;
  value: string;
}

export const RedisBinaryInput = ({
  encoding,
  label,
  multiline = false,
  onEncodingChange,
  onValueChange,
  value,
}: RedisBinaryInputProperties) => {
  const id = `redis-${label.toLowerCase().replaceAll(" ", "-")}`;
  const className =
    "w-full border border-input bg-background px-2.5 text-xs outline-none placeholder:text-muted-foreground focus:border-ring";
  return (
    <label className="mb-3 block" htmlFor={id}>
      <span className="mb-1.5 flex items-center justify-between gap-3 text-[9px] tracking-[0.1em] text-muted-foreground uppercase">
        {label}
        <Select
          items={{ base64url: "Base64url", text: "Text" }}
          onValueChange={(selected) =>
            onEncodingChange(String(selected) as RedisInputEncoding)
          }
          value={encoding}
        >
          <SelectTrigger
            aria-label={`${label} encoding`}
            className="h-6 border-0 bg-transparent px-1 text-[9px] tracking-normal text-muted-foreground normal-case"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent align="end">
            <SelectItem value="text">Text</SelectItem>
            <SelectItem value="base64url">Base64url</SelectItem>
          </SelectContent>
        </Select>
      </span>
      {multiline ? (
        <textarea
          className={`${className} min-h-20 resize-y py-2 leading-5`}
          id={id}
          onChange={(event) => onValueChange(event.target.value)}
          spellCheck={false}
          value={value}
        />
      ) : (
        <input
          className={`${className} h-8`}
          id={id}
          onChange={(event) => onValueChange(event.target.value)}
          spellCheck={false}
          value={value}
        />
      )}
    </label>
  );
};
