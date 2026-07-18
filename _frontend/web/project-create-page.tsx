import { FolderKanban } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";
import { useNavigate } from "react-router";

import { createProject } from "@/api";
import type { Project } from "@/api";
import { Button } from "@/components/ui/button";
import { FormCard, SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageStack } from "@/components/ui/page-stack";

const projectNamePattern = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/u;

export const ProjectCreatePage = ({
  onCreated,
}: {
  onCreated: (project: Project) => void;
}) => {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!projectNamePattern.test(name) || saving) {
      setError("Use lowercase letters, numbers, or hyphens.");
      return;
    }
    setSaving(true);
    setError(null);
    try {
      const project = await createProject(name);
      onCreated(project);
      navigate(`/projects/${project.id}`);
    } catch (createError) {
      setError(
        createError instanceof Error
          ? createError.message
          : "Unable to create project"
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <PageStack className="animate-in duration-200 fade-in slide-in-from-bottom-1">
      <SectionCard className="flex items-center gap-4 px-5 py-5">
        <span className="grid size-9 place-items-center bg-muted">
          <FolderKanban className="size-4" />
        </span>
        <div>
          <h2 className="text-xs font-medium">Create project</h2>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Group the services and data that belong to one application.
          </p>
        </div>
      </SectionCard>

      <FormCard onSubmit={submit}>
        <div className="grid lg:grid-cols-[220px_minmax(16rem,1fr)]">
          <div className="px-5 py-4 lg:border-r lg:border-border">
            <label className="text-[10px] font-medium" htmlFor="project-name">
              Project name
            </label>
            <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
              Lowercase letters, numbers, and hyphens.
            </p>
          </div>
          <div className="px-5 py-4">
            <Input
              autoCapitalize="none"
              autoComplete="off"
              autoFocus
              id="project-name"
              maxLength={63}
              onChange={(event) => setName(event.target.value)}
              placeholder="storefront"
              spellCheck={false}
              value={name}
            />
            {error ? (
              <p
                aria-live="polite"
                className="mt-2 text-[10px] text-destructive"
              >
                {error}
              </p>
            ) : null}
          </div>
        </div>

        <div className="flex justify-end gap-2 border-t border-border px-5 py-4">
          <Button
            disabled={saving}
            onClick={() => navigate("/projects")}
            type="button"
            variant="ghost"
          >
            Cancel
          </Button>
          <Button disabled={saving || name === ""} type="submit">
            {saving ? "Creating…" : "Create project"}
          </Button>
        </div>
      </FormCard>
    </PageStack>
  );
};
