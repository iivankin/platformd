import { Trash2 } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";

export const ResourceDeleteSection = ({
  busy,
  description,
  label,
  name,
  onDelete,
}: {
  busy: boolean;
  description: string;
  label: string;
  name: string;
  onDelete: () => Promise<void>;
}) => {
  const [armed, setArmed] = useState(false);
  const [confirmation, setConfirmation] = useState("");

  return (
    <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
      <div className="px-5 py-4">
        <h3 className="text-[9px] tracking-[0.13em] text-destructive uppercase">
          Delete {label}
        </h3>
        <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
          {description}
        </p>
      </div>
      <div className="border-t border-border px-5 py-4 lg:border-t-0 lg:border-l">
        {armed ? (
          <div className="max-w-lg">
            <p className="text-[10px] leading-4 text-destructive">
              Type {name} to confirm deletion.
            </p>
            <div className="mt-3 flex gap-2">
              <Input
                aria-label={`Confirm deletion of ${name}`}
                autoComplete="off"
                onChange={(event) => setConfirmation(event.target.value)}
                value={confirmation}
              />
              <Button
                disabled={busy || confirmation !== name}
                onClick={() => void onDelete()}
                type="button"
                variant="destructive"
              >
                Delete
              </Button>
              <Button
                disabled={busy}
                onClick={() => {
                  setArmed(false);
                  setConfirmation("");
                }}
                type="button"
                variant="ghost"
              >
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <Button
            disabled={busy}
            onClick={() => setArmed(true)}
            type="button"
            variant="destructive"
          >
            <Trash2 /> Delete {label}
          </Button>
        )}
      </div>
    </SectionCard>
  );
};
