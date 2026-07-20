import { ChevronDown, LoaderCircle, Rocket, Undo2 } from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { pendingResourceChangeDetails } from "@/pending-resource-creation";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import type { PendingServiceSettings } from "@/service-settings-model";
import { serviceSettingsChangeDetails } from "@/service-settings-model";

export const ProjectChangeBar = ({
  applying,
  changes,
  error,
  onApply,
  onDiscard,
  resourceDrafts,
}: {
  applying: boolean;
  changes: PendingServiceSettings[];
  error?: string;
  onApply: () => void;
  onDiscard: () => void;
  resourceDrafts: PendingResourceCreation[];
}) => {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const services = useMemo(
    () =>
      changes.map((change) => ({
        change,
        details: serviceSettingsChangeDetails(change),
      })),
    [changes]
  );
  const resources = useMemo(
    () =>
      resourceDrafts.map((draft) => ({
        details: pendingResourceChangeDetails(draft),
        draft,
      })),
    [resourceDrafts]
  );
  const total =
    services.reduce((sum, service) => sum + service.details.length, 0) +
    resources.reduce((sum, resource) => sum + resource.details.length, 0);
  const affectedResources = services.length + resourceDrafts.length;

  if (total === 0) {
    return null;
  }

  return (
    <div className="pointer-events-auto absolute top-4 left-1/2 z-20 w-[min(38rem,calc(100%-2rem))] -translate-x-1/2 border border-foreground/20 bg-card shadow-lg">
      <div className="flex min-h-12 items-center gap-2 px-3 py-2">
        <div className="mr-auto min-w-0">
          <p className="truncate text-xs font-medium">
            Apply {total} {total === 1 ? "change" : "changes"}
          </p>
          {error ? (
            <p
              className="mt-0.5 truncate text-[9px] text-destructive"
              title={error}
            >
              {error}
            </p>
          ) : (
            <p className="mt-0.5 text-[9px] text-muted-foreground">
              {affectedResources}{" "}
              {affectedResources === 1 ? "resource" : "resources"} will be
              applied
            </p>
          )}
        </div>
        <Button
          aria-expanded={detailsOpen}
          disabled={applying}
          onClick={() => setDetailsOpen((open) => !open)}
          size="sm"
          variant="outline"
        >
          Details <ChevronDown className={detailsOpen ? "rotate-180" : ""} />
        </Button>
        <Button disabled={applying} onClick={onApply} size="sm">
          {applying ? <LoaderCircle className="animate-spin" /> : <Rocket />}
          {applying ? "Deploying…" : "Deploy"}
        </Button>
      </div>

      {detailsOpen ? (
        <div className="max-h-72 overflow-auto border-t border-border bg-background">
          {services.map(({ change, details }) => (
            <section
              className="border-b border-border last:border-b-0"
              key={change.serviceID}
            >
              <header className="flex items-center justify-between bg-muted/25 px-4 py-2.5">
                <span className="text-[10px] font-medium">
                  {change.serviceName}
                </span>
                <span className="font-mono text-[9px] text-muted-foreground">
                  {details.length} {details.length === 1 ? "change" : "changes"}
                </span>
              </header>
              {details.map((detail) => (
                <div
                  className="grid grid-cols-[9rem_minmax(0,1fr)] border-t border-border px-4 py-2 text-[9px]"
                  key={detail.id}
                >
                  <span className="text-muted-foreground">{detail.label}</span>
                  <span className="truncate font-mono" title={detail.detail}>
                    {detail.detail}
                  </span>
                </div>
              ))}
            </section>
          ))}
          {resources.map(({ details, draft }) => (
            <section
              className="border-b border-border last:border-b-0"
              key={draft.id}
            >
              <header className="flex items-center justify-between bg-muted/25 px-4 py-2.5">
                <span className="text-[10px] font-medium">
                  {draft.input.name}
                </span>
                <span className="font-mono text-[9px] text-muted-foreground">
                  {details.length} {details.length === 1 ? "change" : "changes"}
                </span>
              </header>
              {details.map((detail) => (
                <div
                  className="grid grid-cols-[9rem_minmax(0,1fr)] border-t border-border px-4 py-2 text-[9px]"
                  key={detail.id}
                >
                  <span className="text-muted-foreground">{detail.label}</span>
                  <span className="truncate font-mono" title={detail.detail}>
                    {detail.detail}
                  </span>
                </div>
              ))}
            </section>
          ))}
          <div className="flex items-center justify-between px-3 py-2">
            <span className="text-[9px] text-muted-foreground">
              Changes stay local until deployed.
            </span>
            <Button
              disabled={applying}
              onClick={onDiscard}
              size="sm"
              variant="ghost"
            >
              <Undo2 /> Discard all
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  );
};
