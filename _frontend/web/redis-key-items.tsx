import { Trash2 } from "lucide-react";
import { useState } from "react";

import type { RedisMutationInput, RedisPreview } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { redisDisplayValue } from "@/redis-data-utils";

interface RedisKeyItemsProperties {
  apply: (input: RedisMutationInput) => Promise<void>;
  busy: boolean;
  keyBase64: string;
  preview: RedisPreview;
}

const removalMutation = (
  type: string,
  key: string,
  value: string,
  listCount: number
): RedisMutationInput | null => {
  switch (type) {
    case "hash": {
      return { field: value, key, operation: "hash_delete" };
    }
    case "list": {
      return { count: listCount, key, operation: "list_remove", value };
    }
    case "set": {
      return { key, member: value, operation: "set_remove" };
    }
    case "zset": {
      return { key, member: value, operation: "zset_remove" };
    }
    case "stream": {
      return { key, operation: "stream_delete", streamId: value };
    }
    default: {
      return null;
    }
  }
};

export const RedisKeyItems = ({
  apply,
  busy,
  keyBase64,
  preview,
}: RedisKeyItemsProperties) => {
  const [count, setCount] = useState("1");
  const itemDeletionEnabled = preview.type !== "string";

  return (
    <>
      <div className="max-h-72 overflow-y-auto">
        {preview.items.length === 0 ? (
          <p className="px-4 py-5 text-[10px] text-muted-foreground">
            No preview items.
          </p>
        ) : (
          preview.items.map((item, itemIndex) => {
            const [first] = item.values;
            const mutation = first
              ? removalMutation(
                  preview.type,
                  keyBase64,
                  first.base64,
                  Number(count)
                )
              : null;
            return (
              <div
                className="flex items-start gap-3 border-b border-border px-4 py-3 last:border-b-0"
                key={`${itemIndex.toString()}:${first?.base64 ?? ""}`}
              >
                <span className="w-8 shrink-0 text-[9px] text-muted-foreground">
                  {itemIndex}
                </span>
                <div className="min-w-0 flex-1 space-y-1">
                  {item.values.map((itemValue, valueIndex) => (
                    <code
                      className="block min-w-0 text-[10px] leading-4 break-all"
                      key={`${valueIndex.toString()}:${itemValue.base64}`}
                    >
                      {redisDisplayValue(itemValue)}
                    </code>
                  ))}
                </div>
                {itemDeletionEnabled && mutation ? (
                  <Button
                    aria-label={`Delete ${preview.type} item ${itemIndex + 1}`}
                    disabled={busy}
                    onClick={() => void apply(mutation)}
                    size="icon"
                    variant="ghost"
                  >
                    <Trash2 />
                  </Button>
                ) : null}
              </div>
            );
          })
        )}
      </div>
      {preview.type === "list" ? (
        <label
          className="flex items-center gap-2 border-t border-border px-4 py-3 text-[9px] text-muted-foreground"
          htmlFor="redis-remove-count"
        >
          Remove matching values, count
          <Input
            className="w-20"
            id="redis-remove-count"
            onChange={(event) => setCount(event.target.value)}
            type="number"
            value={count}
          />
        </label>
      ) : null}
    </>
  );
};
