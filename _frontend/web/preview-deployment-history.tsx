import { ExternalLink, ScrollText } from "lucide-react";

import type { PreviewDeployment } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";

const statusColor: Record<PreviewDeployment["status"], string> = {
  active: "bg-emerald-500",
  building: "bg-sky-500",
  failed: "bg-destructive",
  interrupted: "bg-amber-500",
  skipped: "bg-muted-foreground",
  stopped: "bg-muted-foreground",
};

export const PreviewDeploymentHistory = ({
  onViewLogs,
  previews,
}: {
  onViewLogs: (preview: PreviewDeployment) => void;
  previews: PreviewDeployment[];
}) => {
  if (previews.length === 0) {
    return null;
  }
  return (
    <SectionCard>
      <header className="flex items-center justify-between border-b border-border px-4 py-3">
        <div>
          <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Pull request previews
          </h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Isolated deployments are retained with their logs for 14 days.
          </p>
        </div>
        <span className="text-[9px] text-muted-foreground">
          {previews.length} total
        </span>
      </header>
      {previews.map((preview) => (
        <div
          className="grid min-h-16 grid-cols-[minmax(0,1fr)_auto] items-center gap-4 border-b border-border px-4 py-3 last:border-b-0"
          key={preview.id}
        >
          <div className="flex min-w-0 items-start gap-3">
            <span
              className={`mt-1.5 size-1.5 shrink-0 ${statusColor[preview.status]}`}
            />
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2 text-[10px]">
                <span className="font-medium">
                  PR #{preview.pullRequestNumber}
                </span>
                <span className="text-muted-foreground capitalize">
                  {preview.status}
                </span>
                <code className="text-muted-foreground">
                  {preview.sourceRevision.slice(0, 12)}
                </code>
              </div>
              <p className="mt-1 truncate text-[9px] text-muted-foreground">
                {preview.hostname} → :{preview.targetPort} ·{" "}
                {new Date(preview.createdAt).toLocaleString()}
              </p>
              {preview.errorMessage ? (
                <p className="mt-1 text-[9px] text-destructive">
                  {preview.errorMessage}
                </p>
              ) : null}
            </div>
          </div>
          <div className="flex items-center gap-1">
            {preview.status === "active" ? (
              <a
                aria-label={`Open preview for pull request ${preview.pullRequestNumber}`}
                className="grid size-8 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground [&_svg]:size-4"
                href={`https://${preview.hostname}`}
                rel="noreferrer"
                target="_blank"
              >
                <ExternalLink />
              </a>
            ) : null}
            <Button
              aria-label={`View logs for pull request ${preview.pullRequestNumber}`}
              onClick={() => onViewLogs(preview)}
              size="icon"
              variant="ghost"
            >
              <ScrollText />
            </Button>
          </div>
        </div>
      ))}
    </SectionCard>
  );
};
