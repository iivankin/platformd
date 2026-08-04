import { Power } from "lucide-react";

import { SectionCard } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import type { BeforeDeployDraft } from "@/service-before-deploy-model";

const ToggleAction = ({
  enabled,
  label,
  onChange,
}: {
  enabled: boolean;
  label: string;
  onChange: (enabled: boolean) => void;
}) => (
  <button
    aria-pressed={enabled}
    className="flex min-h-11 w-full items-center gap-3 px-4 text-left hover:bg-muted/40"
    onClick={() => onChange(!enabled)}
    type="button"
  >
    <span
      className={`grid size-5 place-items-center border ${enabled ? "border-emerald-500/50 bg-emerald-500/10 text-emerald-600" : "border-border text-muted-foreground"}`}
    >
      <Power className="size-2.5" />
    </span>
    <span className="text-[9px]">{label}</span>
    <span className="ml-auto text-[9px] text-muted-foreground">
      {enabled ? "On" : "Off"}
    </span>
  </button>
);

export const ServiceBeforeDeploy = ({
  domains,
  draft,
  onChange,
}: {
  domains: readonly { hostname: string }[];
  draft: BeforeDeployDraft;
  onChange: (draft: BeforeDeployDraft) => void;
}) => (
  <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
    <div className="px-5 py-4">
      <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
        Before deploy
      </h3>
      <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
        Runs after the image is ready and before the new container starts.
        Failure keeps the previous deployment active.
      </p>
      <p className="mt-3 font-mono text-[8px] leading-4 text-muted-foreground">
        01 command → 02 purge
      </p>
    </div>
    <div className="divide-y divide-border border-t border-border lg:border-t-0 lg:border-l">
      <section className="grid grid-cols-[2.25rem_minmax(0,1fr)]">
        <span className="grid place-items-center border-r border-border font-mono text-[8px] text-muted-foreground">
          01
        </span>
        <div>
          <ToggleAction
            enabled={draft.commandEnabled}
            label="Run a command in the new image"
            onChange={(commandEnabled) =>
              onChange({ ...draft, commandEnabled })
            }
          />
          {draft.commandEnabled ? (
            <div className="border-t border-border px-4 py-3">
              <label
                className="grid gap-1.5 text-[9px] text-muted-foreground"
                htmlFor="before-deploy-command"
              >
                Shell command
                <textarea
                  autoCapitalize="none"
                  autoComplete="off"
                  className="min-h-20 resize-y border border-input bg-background px-2.5 py-2 font-mono text-[10px] leading-4 text-foreground outline-none placeholder:text-muted-foreground/55 focus-visible:border-ring focus-visible:ring-1 focus-visible:ring-ring"
                  id="before-deploy-command"
                  onChange={(event) =>
                    onChange({ ...draft, command: event.target.value })
                  }
                  placeholder="bun run migrate"
                  spellCheck={false}
                  value={draft.command}
                />
              </label>
              <p className="mt-2 text-[8px] leading-4 text-muted-foreground">
                Runs as /bin/sh -lc with project networking and runtime
                variables. Volumes are not mounted. Timeout: 30 minutes.
              </p>
            </div>
          ) : null}
        </div>
      </section>
      <section className="grid grid-cols-[2.25rem_minmax(0,1fr)]">
        <span className="grid place-items-center border-r border-border font-mono text-[8px] text-muted-foreground">
          02
        </span>
        <div>
          <ToggleAction
            enabled={draft.cloudflareEnabled}
            label="Purge Cloudflare cache by hostname"
            onChange={(cloudflareEnabled) =>
              onChange({ ...draft, cloudflareEnabled })
            }
          />
          {draft.cloudflareEnabled ? (
            <div className="grid gap-2 border-t border-border px-4 py-3">
              {domains.length ? (
                domains.map((domain) => {
                  const checked = draft.cloudflareHostnames.includes(
                    domain.hostname
                  );
                  return (
                    <label
                      className="flex cursor-pointer items-center gap-2 py-1 text-[9px]"
                      htmlFor={`before-deploy-cloudflare-${domain.hostname}`}
                      key={domain.hostname}
                    >
                      <Checkbox
                        checked={checked}
                        id={`before-deploy-cloudflare-${domain.hostname}`}
                        onCheckedChange={(enabled) =>
                          onChange({
                            ...draft,
                            cloudflareHostnames: enabled
                              ? [...draft.cloudflareHostnames, domain.hostname]
                              : draft.cloudflareHostnames.filter(
                                  (hostname) => hostname !== domain.hostname
                                ),
                          })
                        }
                      />
                      {domain.hostname}
                    </label>
                  );
                })
              ) : (
                <p className="text-[8px] text-muted-foreground">
                  Attach an HTTP domain first.
                </p>
              )}
            </div>
          ) : null}
        </div>
      </section>
    </div>
  </SectionCard>
);
