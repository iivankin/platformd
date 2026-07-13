import { Button } from "@/components/ui/button";
import type { DatabaseVersionChangeState } from "@/use-database-version-change";

interface DatabaseVersionPreviewProperties {
  change: DatabaseVersionChangeState;
}

const formatBytes = (bytes: number) => {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
};

export const DatabaseVersionPreview = ({
  change,
}: DatabaseVersionPreviewProperties) => {
  if (!change.preview) {
    return null;
  }
  const running = change.operation?.status === "running";
  const canStart =
    change.preview.ready &&
    change.preview.targetTag === change.targetTag.trim() &&
    change.acknowledged &&
    !running;

  return (
    <div className="mt-4 border-t border-border pt-3">
      <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-[9px]">
        <div>
          <dt className="text-muted-foreground">Source</dt>
          <dd className="mt-1 font-mono break-all">
            {change.preview.sourceTag} · {change.preview.sourceDigest}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Target</dt>
          <dd className="mt-1 font-mono break-all">
            {change.preview.targetTag} · {change.preview.targetDigest}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Current data</dt>
          <dd className="mt-1">
            {formatBytes(change.preview.currentDataBytes)}
          </dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Free space</dt>
          <dd className="mt-1">
            {formatBytes(change.preview.availableFreeBytes)} available ·{" "}
            {formatBytes(change.preview.requiredFreeBytes)} required
          </dd>
        </div>
      </dl>

      {change.preview.blocker ? (
        <p className="mt-3 text-[9px] text-destructive">
          {change.preview.blocker === "same_digest"
            ? "This exact digest is already active."
            : "There is not enough free space for the second database volume."}
        </p>
      ) : null}

      <p className="mt-3 text-[9px] leading-4 text-amber-600 dark:text-amber-400">
        The database becomes unavailable during direct transfer; duration is not
        estimated. Platformd creates a new volume and does not infer upgrade or
        downgrade compatibility.
      </p>
      <p className="mt-2 text-[9px] leading-4 text-destructive">
        After the new image is published, the old volume is deleted immediately.
        Data rollback then requires a complete remote backup.
      </p>
      <label className="mt-3 flex items-start gap-2 text-[9px] leading-4">
        <input
          checked={change.acknowledged}
          className="mt-0.5 size-3 accent-primary"
          disabled={!change.preview.ready || running}
          onChange={(event) => change.setAcknowledged(event.target.checked)}
          type="checkbox"
        />
        I understand the downtime and post-publication rollback risk for this
        exact target digest.
      </label>
      <Button
        className="mt-3"
        disabled={!canStart}
        onClick={() => void change.start()}
        size="sm"
        type="button"
        variant="destructive"
      >
        {running ? "Changing image…" : "Change image"}
      </Button>
    </div>
  );
};
