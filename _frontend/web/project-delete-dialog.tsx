import { AlertDialog } from "@base-ui/react/alert-dialog";
import { LoaderCircle, Trash2, X } from "lucide-react";
import { useState } from "react";

import { deleteProject } from "@/api";
import type { Project } from "@/api";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";

const resourceCount = (project: Project) =>
  project.serviceCount +
  project.postgresCount +
  project.redisCount +
  project.objectStoreCount +
  project.networkGatewayCount;

export const ProjectDeleteDialog = ({
  onDeleted,
  project,
}: {
  onDeleted: (projectID: string) => void;
  project: Project;
}) => {
  const [open, setOpen] = useState(false);
  const [confirmation, setConfirmation] = useState("");
  const [deleteBackups, setDeleteBackups] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [error, setError] = useState<string>();
  const count = resourceCount(project);

  const reset = () => {
    setConfirmation("");
    setDeleteBackups(false);
    setError(undefined);
  };

  const remove = async () => {
    if (deleting || confirmation !== project.name) {
      return;
    }
    setDeleting(true);
    setError(undefined);
    try {
      await deleteProject(project.id, {
        deleteBackups,
        expectedName: confirmation,
      });
      setOpen(false);
      onDeleted(project.id);
    } catch (deleteError) {
      setError(
        deleteError instanceof Error
          ? deleteError.message
          : "Unable to delete project"
      );
    } finally {
      setDeleting(false);
    }
  };

  return (
    <>
      <Button
        aria-label="Delete project"
        onClick={() => setOpen(true)}
        size="icon"
        title="Delete project"
        variant="ghost"
      >
        <Trash2 />
      </Button>
      <AlertDialog.Root
        onOpenChange={(nextOpen) => {
          if (deleting) {
            return;
          }
          setOpen(nextOpen);
          if (!nextOpen) {
            reset();
          }
        }}
        open={open}
      >
        <AlertDialog.Portal>
          <AlertDialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
          <AlertDialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
            <AlertDialog.Popup className="w-full max-w-xl border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out">
              <header className="flex items-start justify-between gap-5 border-b border-border px-5 py-4">
                <div>
                  <AlertDialog.Title className="text-sm font-medium">
                    Delete {project.name}
                  </AlertDialog.Title>
                  <AlertDialog.Description className="mt-1.5 text-xs leading-5 text-muted-foreground">
                    This permanently removes the project, its {count} canvas
                    {count === 1 ? " resource" : " resources"}, and all owned
                    volumes.
                  </AlertDialog.Description>
                </div>
                <AlertDialog.Close
                  aria-label="Close"
                  className="flex size-8 shrink-0 items-center justify-center text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
                  disabled={deleting}
                >
                  <X className="size-4" />
                </AlertDialog.Close>
              </header>

              <label
                className="flex cursor-pointer items-start gap-3 border-b border-border px-5 py-4"
                htmlFor="delete-project-backups"
              >
                <Checkbox
                  checked={deleteBackups}
                  disabled={deleting}
                  id="delete-project-backups"
                  onCheckedChange={setDeleteBackups}
                />
                <span className="min-w-0">
                  <span className="block text-xs font-medium">
                    Delete remote resource backups
                  </span>
                  <span className="mt-1 block text-[10px] leading-4 text-muted-foreground">
                    Permanently removes restore points for this project from
                    every configured backup storage. Installation-wide control
                    snapshots follow their own retention policy.
                  </span>
                </span>
              </label>

              <section className="px-5 py-4">
                <label className="text-[10px] tracking-[0.12em] text-muted-foreground uppercase">
                  Type {project.name} to confirm
                  <Input
                    autoComplete="off"
                    autoFocus
                    className="mt-2"
                    disabled={deleting}
                    onChange={(event) => setConfirmation(event.target.value)}
                    placeholder={project.name}
                    value={confirmation}
                  />
                </label>
                {error ? (
                  <p className="mt-3 text-xs leading-5 text-destructive">
                    {error}
                  </p>
                ) : null}
              </section>

              <footer className="flex items-center justify-end gap-2 border-t border-border px-5 py-4">
                <AlertDialog.Close
                  className="inline-flex h-8 items-center justify-center border border-border px-2.5 text-xs font-medium text-foreground outline-none hover:bg-muted focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
                  disabled={deleting}
                >
                  Cancel
                </AlertDialog.Close>
                <Button
                  disabled={confirmation !== project.name || deleting}
                  onClick={() => void remove()}
                  variant="destructive"
                >
                  {deleting ? (
                    <LoaderCircle className="animate-spin" />
                  ) : (
                    <Trash2 />
                  )}
                  Delete project
                </Button>
              </footer>
            </AlertDialog.Popup>
          </AlertDialog.Viewport>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </>
  );
};
