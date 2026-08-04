import { Cable, LoaderCircle, Save } from "lucide-react";
import { useState } from "react";

import type { PortForwardAccess } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import {
  GitHubActionExampleDialog,
  portForwardActionExample,
} from "@/github-action-example-dialog";
import type { PortForwardExampleKind } from "@/github-action-example-dialog";

export interface PortForwardDraft {
  repository: string;
  workflows: string[];
}

export interface PortForwardExample {
  kind?: PortForwardExampleKind;
  localPort?: number;
  port: number;
  projectName: string;
  resourceName: string;
}

export const emptyPortForwardDraft = (): PortForwardDraft => ({
  repository: "",
  workflows: [],
});

export const portForwardDraft = (
  value?: PortForwardAccess
): PortForwardDraft => ({
  repository: value?.repository ?? "",
  workflows: value?.workflows ?? [],
});

export const parsePortForward = (
  draft: PortForwardDraft
): PortForwardAccess | undefined => {
  const repository = draft.repository.trim().toLowerCase();
  if (!repository) {
    return;
  }
  return {
    repository,
    workflows: draft.workflows.map((value) => value.trim()).filter(Boolean),
  };
};

const draftsEqual = (left: PortForwardDraft, right: PortForwardDraft) =>
  left.repository === right.repository &&
  left.workflows.join("\0") === right.workflows.join("\0");

export const ServicePortForwardSettings = ({
  draft,
  example,
  idPrefix = "service",
  onDraftChange,
}: {
  draft: PortForwardDraft;
  example?: PortForwardExample;
  idPrefix?: string;
  onDraftChange: (draft: PortForwardDraft) => void;
}) => {
  const update = (values: Partial<PortForwardDraft>) =>
    onDraftChange({ ...draft, ...values });

  return (
    <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
      <div className="px-5 py-4">
        <h3 className="flex items-center gap-2 text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
          <Cable className="size-3" /> GitHub Actions port forward
        </h3>
        <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
          Allow workflows from a repository to open temporary tunnels with OIDC.
          Leave empty to require an admin API token.
        </p>
      </div>
      <div className="grid gap-3 border-t border-border p-4 md:grid-cols-2 lg:border-t-0 lg:border-l">
        <label
          className="grid gap-1.5 text-[9px] text-muted-foreground"
          htmlFor={`${idPrefix}-port-forward-repository`}
        >
          Repository
          <Input
            autoCapitalize="none"
            autoComplete="off"
            id={`${idPrefix}-port-forward-repository`}
            onChange={(event) =>
              update({ repository: event.target.value.toLowerCase().trim() })
            }
            placeholder="org/backend"
            spellCheck={false}
            value={draft.repository}
          />
        </label>
        <label
          className="grid gap-1.5 text-[9px] text-muted-foreground"
          htmlFor={`${idPrefix}-port-forward-workflows`}
        >
          Allowed workflow files · optional
          <Input
            autoCapitalize="none"
            autoComplete="off"
            id={`${idPrefix}-port-forward-workflows`}
            onChange={(event) =>
              update({
                workflows: event.target.value
                  .split(",")
                  .map((value) => value.trim())
                  .filter(Boolean),
              })
            }
            placeholder="integration.yml, e2e.yaml"
            spellCheck={false}
            value={draft.workflows.join(", ")}
          />
        </label>
        <div className="flex flex-wrap items-center justify-between gap-3 md:col-span-2">
          <p className="text-[9px] leading-4 text-muted-foreground">
            Empty workflow list allows any workflow in the repository.
          </p>
          {example ? (
            <GitHubActionExampleDialog
              description="GitHub Actions opens a short-lived localhost tunnel to this resource with OIDC. No admin API token is required when the repository above is allowed."
              example={portForwardActionExample(example)}
              notes={
                <>
                  Bypass Cloudflare Access for <code>/public/*</code>. The
                  workflow needs <code>permissions: id-token: write</code>.
                </>
              }
              steps={[
                "Allow the GitHub repository (and optional workflow files) above.",
                "Put connection secrets (if any) in the repository.",
                "Run the workflow: the action tunnels the resource, then later steps use localhost.",
              ]}
              title="GitHub Actions port forward"
            />
          ) : null}
        </div>
      </div>
    </SectionCard>
  );
};

export const ResourcePortForwardSettings = ({
  example,
  idPrefix,
  onSaved,
  onSave,
  updatedAt,
  value,
}: {
  example: PortForwardExample;
  idPrefix: string;
  onSave: (
    portForward: PortForwardAccess | undefined,
    expectedUpdatedAt: number
  ) => Promise<void>;
  onSaved?: () => void;
  updatedAt: number;
  value?: PortForwardAccess;
}) => {
  const baseline = portForwardDraft(value);
  const [draft, setDraft] = useState(baseline);
  const [baselineAt, setBaselineAt] = useState(updatedAt);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (updatedAt !== baselineAt) {
    setBaselineAt(updatedAt);
    setDraft(baseline);
    setError(null);
  }

  const dirty = !draftsEqual(draft, baseline);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      await onSave(parsePortForward(draft), updatedAt);
      onSaved?.();
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to save port-forward settings"
      );
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="grid gap-3">
      <ServicePortForwardSettings
        draft={draft}
        example={example}
        idPrefix={idPrefix}
        onDraftChange={setDraft}
      />
      <div className="flex items-center gap-3 px-1">
        <Button disabled={!dirty || busy} onClick={() => void save()} size="sm">
          {busy ? <LoaderCircle className="animate-spin" /> : <Save />}
          Save port forward
        </Button>
        {error ? (
          <p aria-live="polite" className="text-[9px] text-destructive">
            {error}
          </p>
        ) : null}
      </div>
    </div>
  );
};
