import {
  Boxes,
  Database,
  FolderKanban,
  HardDrive,
  Plus,
  Server,
  X,
} from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";

import { createProject } from "@/api";
import type { Project } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const projectNamePattern = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/u;
const dateFormatter = new Intl.DateTimeFormat(undefined, {
  day: "2-digit",
  month: "short",
  year: "numeric",
});

const resourceCount = (project: Project) =>
  project.serviceCount +
  project.postgresCount +
  project.redisCount +
  project.objectStoreCount;

interface ProjectsPageProperties {
  loadError: string | null;
  loading: boolean;
  onCreated: (project: Project) => void;
  projects: Project[];
}

export const ProjectsPage = ({
  loadError,
  loading,
  onCreated,
  projects,
}: ProjectsPageProperties) => {
  const navigate = useNavigate();
  const [searchParameters, setSearchParameters] = useSearchParams();
  const [createOpen, setCreateOpen] = useState(false);
  const creating = createOpen || searchParameters.get("new") === "1";
  const [name, setName] = useState("");
  const [mutationError, setMutationError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const closeCreate = () => {
    setCreateOpen(false);
    setMutationError(null);
    if (searchParameters.size > 0) {
      setSearchParameters({}, { replace: true });
    }
  };

  const openCreate = () => {
    setCreateOpen(true);
    if (searchParameters.size > 0) {
      setSearchParameters({}, { replace: true });
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!projectNamePattern.test(name) || saving) {
      setMutationError(
        "Use a lowercase DNS label with letters, digits, or hyphens."
      );
      return;
    }
    setSaving(true);
    setMutationError(null);
    try {
      const created = await createProject(name);
      onCreated(created);
      setName("");
      setCreateOpen(false);
      navigate(`/projects/${created.id}`);
    } catch (error) {
      setMutationError(
        error instanceof Error ? error.message : "Unable to create project"
      );
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="enter-row">
      <section className="flex min-h-14 items-center justify-between gap-4 border-b border-border px-5 py-3">
        <div>
          <p className="text-xs font-medium">Isolation boundaries</p>
          <p className="mt-1 text-[10px] text-muted-foreground">
            {projects.length} project{projects.length === 1 ? "" : "s"} ·{" "}
            {projects.reduce(
              (total, project) => total + resourceCount(project),
              0
            )}{" "}
            resources
          </p>
        </div>
        <Button onClick={openCreate} size="sm">
          <Plus />
          New project
        </Button>
      </section>

      {creating ? (
        <form
          className="grid border-b border-border bg-muted/20 lg:grid-cols-[1fr_320px]"
          onSubmit={submit}
        >
          <div className="border-b border-border px-5 py-5 lg:border-r lg:border-b-0">
            <div className="flex items-start gap-3">
              <div className="grid size-8 shrink-0 place-items-center border border-border bg-background">
                <FolderKanban className="size-4 text-muted-foreground" />
              </div>
              <div>
                <h2 className="text-xs font-medium">Create project</h2>
                <p className="mt-1 max-w-xl text-[10px] leading-4 text-muted-foreground">
                  A project gets its own bridge network, DNS view, services, and
                  managed resources.
                </p>
              </div>
            </div>
          </div>
          <div className="px-5 py-4">
            <label
              className="text-[10px] tracking-[0.12em] text-muted-foreground uppercase"
              htmlFor="project-name"
            >
              Project name
            </label>
            <Input
              autoCapitalize="none"
              autoComplete="off"
              className="mt-2"
              id="project-name"
              maxLength={63}
              onChange={(event) => setName(event.target.value)}
              placeholder="shop"
              spellCheck={false}
              value={name}
            />
            <p className="mt-2 truncate text-[10px] text-muted-foreground">
              namespace: {name || "project"}.internal
            </p>
            {mutationError ? (
              <p
                aria-live="polite"
                className="mt-2 text-[10px] text-destructive"
              >
                {mutationError}
              </p>
            ) : null}
            <div className="mt-4 flex justify-end gap-2">
              <Button
                onClick={closeCreate}
                size="sm"
                type="button"
                variant="ghost"
              >
                <X />
                Cancel
              </Button>
              <Button disabled={saving} size="sm" type="submit">
                {saving ? "Creating…" : "Create project"}
              </Button>
            </div>
          </div>
        </form>
      ) : null}

      {loadError ? (
        <section className="border-b border-destructive/30 bg-destructive/5 px-5 py-4 text-xs text-destructive">
          {loadError}
        </section>
      ) : null}

      {loading ? (
        <section className="border-b border-border px-5 py-10 text-center text-[10px] tracking-[0.14em] text-muted-foreground uppercase">
          Loading projects
        </section>
      ) : null}

      {!loading && !loadError && projects.length === 0 ? (
        <section className="grid min-h-80 place-items-center border-b border-border px-8 py-16 text-center">
          <div className="max-w-md">
            <Boxes className="mx-auto size-7 text-muted-foreground" />
            <h2 className="mt-5 text-sm font-medium">No projects yet</h2>
            <p className="mt-2 text-xs leading-5 text-muted-foreground">
              Start with one project. Network and internal DNS are provisioned
              by the control plane.
            </p>
            <Button className="mt-5" onClick={openCreate}>
              <Plus />
              Create project
            </Button>
          </div>
        </section>
      ) : null}

      {projects.length > 0 ? (
        <section className="border-b border-border">
          <div className="grid grid-cols-[minmax(180px,1.3fr)_minmax(240px,1fr)_100px] border-b border-border bg-muted/30 px-5 py-2 text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
            <span>Project</span>
            <span>Resources</span>
            <span>Updated</span>
          </div>
          <div className="[content-visibility:auto]">
            {projects.map((project) => (
              <Link
                className="grid min-h-14 grid-cols-[minmax(180px,1.3fr)_minmax(240px,1fr)_100px] items-center border-b border-border px-5 py-3 last:border-b-0 hover:bg-muted/25"
                key={project.id}
                to={`/projects/${project.id}`}
              >
                <div className="min-w-0">
                  <div className="truncate text-xs font-medium">
                    {project.name}
                  </div>
                  <div className="mt-1 truncate text-[9px] text-muted-foreground">
                    {project.name}.internal
                  </div>
                </div>
                <div className="flex items-center gap-4 text-[10px] text-muted-foreground">
                  <span className="flex items-center gap-1.5" title="Services">
                    <Server className="size-3" />
                    {project.serviceCount}
                  </span>
                  <span
                    className="flex items-center gap-1.5"
                    title="PostgreSQL and Redis"
                  >
                    <Database className="size-3" />
                    {project.postgresCount + project.redisCount}
                  </span>
                  <span
                    className="flex items-center gap-1.5"
                    title="Object stores"
                  >
                    <HardDrive className="size-3" />
                    {project.objectStoreCount}
                  </span>
                </div>
                <time
                  className="text-[9px] text-muted-foreground"
                  dateTime={new Date(project.updatedAt).toISOString()}
                >
                  {dateFormatter.format(project.updatedAt)}
                </time>
              </Link>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
};
