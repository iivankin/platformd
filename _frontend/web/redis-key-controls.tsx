import { Clock3, Trash2 } from "lucide-react";
import { useState } from "react";

import type { RedisMutationInput } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface RedisKeyControlsProperties {
  apply: (input: RedisMutationInput) => Promise<void>;
  busy: boolean;
  keyBase64: string;
}

export const RedisKeyControls = ({
  apply,
  busy,
  keyBase64,
}: RedisKeyControlsProperties) => {
  const [ttlSeconds, setTTLSeconds] = useState("");
  const [deleteConfirm, setDeleteConfirm] = useState(false);
  return (
    <div className="border-t border-border px-4 py-4">
      <div className="grid grid-cols-[1fr_auto_auto] gap-2">
        <Input
          aria-label="TTL seconds"
          min={1}
          onChange={(event) => setTTLSeconds(event.target.value)}
          placeholder="TTL seconds"
          type="number"
          value={ttlSeconds}
        />
        <Button
          disabled={busy || ttlSeconds === ""}
          onClick={() =>
            void apply({
              key: keyBase64,
              operation: "ttl_set",
              ttlMillis: Number(ttlSeconds) * 1000,
            })
          }
          size="sm"
          type="button"
          variant="outline"
        >
          <Clock3 />
          Set TTL
        </Button>
        <Button
          disabled={busy}
          onClick={() => void apply({ key: keyBase64, operation: "ttl_clear" })}
          size="sm"
          type="button"
          variant="ghost"
        >
          Persist
        </Button>
      </div>
      <div className="mt-4 flex items-center gap-2">
        <Button
          disabled={busy}
          onClick={() => {
            if (deleteConfirm) {
              void apply({ key: keyBase64, operation: "key_delete" });
              return;
            }
            setDeleteConfirm(true);
          }}
          size="sm"
          type="button"
          variant="destructive"
        >
          <Trash2 />
          {deleteConfirm ? "Confirm delete" : "Delete key"}
        </Button>
        {deleteConfirm ? (
          <Button
            onClick={() => setDeleteConfirm(false)}
            size="sm"
            type="button"
            variant="ghost"
          >
            Cancel
          </Button>
        ) : null}
      </div>
    </div>
  );
};
