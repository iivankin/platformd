import { Dialog } from "@base-ui/react/dialog";
import {
  Check,
  Clipboard,
  KeyRound,
  LoaderCircle,
  ShieldAlert,
  X,
} from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { createAPIToken } from "@/api";
import type { APIToken, Project } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { FormField } from "@/form-field";

const allProjects = "__all-projects__";

export const APITokenCreateDialog = ({ projects }: { projects: Project[] }) => {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [role, setRole] = useState<APIToken["role"]>("read");
  const [projectID, setProjectID] = useState("");
  const [revealed, setRevealed] = useState<APIToken>();
  const [creating, setCreating] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string>();

  const reset = () => {
    setName("");
    setRole("read");
    setProjectID("");
    setRevealed(undefined);
    setCopied(false);
    setError(undefined);
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (creating || name.trim() === "") {
      return;
    }
    setCreating(true);
    setError(undefined);
    try {
      const token = await createAPIToken({
        name: name.trim(),
        ...(projectID ? { projectId: projectID } : {}),
        role,
      });
      setRevealed(token);
      setCopied(false);
    } catch (createError) {
      setError(
        createError instanceof Error
          ? createError.message
          : "Unable to create API token"
      );
    } finally {
      setCreating(false);
    }
  };

  const copy = async () => {
    if (!revealed?.token) {
      return;
    }
    try {
      await navigator.clipboard.writeText(revealed.token);
      setCopied(true);
      setError(undefined);
    } catch {
      setError("Clipboard access failed. Select and copy the token manually.");
    }
  };

  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        if (creating) {
          return;
        }
        setOpen(nextOpen);
        if (!nextOpen) {
          reset();
        }
      }}
      open={open}
    >
      <Dialog.Trigger
        render={
          <Button className="mt-3" size="sm">
            <KeyRound /> Create a token
          </Button>
        }
      />
      <Dialog.Portal>
        <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
        <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
          <Dialog.Popup className="w-full max-w-xl border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out">
            <header className="flex items-start justify-between gap-5 border-b border-border px-5 py-4">
              <div>
                <Dialog.Title className="text-sm font-medium">
                  Create API token
                </Dialog.Title>
                <Dialog.Description className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
                  Use this credential with REST or MCP clients.
                </Dialog.Description>
              </div>
              <Dialog.Close
                aria-label="Close token dialog"
                className="flex size-8 shrink-0 items-center justify-center text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
                disabled={creating}
              >
                <X className="size-4" />
              </Dialog.Close>
            </header>

            {revealed?.token ? (
              <div className="bg-emerald-500/5 px-5 py-5">
                <div className="flex items-start gap-3">
                  <Check className="mt-0.5 size-4 shrink-0 text-emerald-600" />
                  <div className="min-w-0 flex-1">
                    <p className="text-xs font-medium">Save this token now</p>
                    <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
                      The secret cannot be displayed again after this dialog
                      closes.
                    </p>
                  </div>
                </div>
                <code className="mt-4 block overflow-x-auto border border-emerald-500/30 bg-background px-3 py-2 text-[10px] leading-5 select-all">
                  {revealed.token}
                </code>
                <div className="mt-4 flex justify-end gap-2">
                  <Button onClick={() => void copy()} variant="outline">
                    {copied ? <Check /> : <Clipboard />}
                    {copied ? "Copied" : "Copy token"}
                  </Button>
                  <Dialog.Close
                    render={<Button variant="default">Done</Button>}
                  />
                </div>
              </div>
            ) : (
              <form id="api-token-create-form" onSubmit={submit}>
                <div className="grid gap-4 px-5 py-5 sm:grid-cols-2">
                  <div className="sm:col-span-2">
                    <FormField label="Token name" name="token-name">
                      <Input
                        autoComplete="off"
                        autoFocus
                        id="token-name"
                        onChange={(event) => setName(event.target.value)}
                        placeholder="deploy-bot"
                        value={name}
                      />
                    </FormField>
                  </div>
                  <FormField label="Role" name="token-role">
                    <Select
                      items={{ admin: "Admin", read: "Read" }}
                      onValueChange={(value) =>
                        setRole(String(value) as APIToken["role"])
                      }
                      value={role}
                    >
                      <SelectTrigger
                        className="h-8 w-full text-xs"
                        id="token-role"
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent align="start">
                        <SelectItem value="read">Read</SelectItem>
                        <SelectItem value="admin">Admin</SelectItem>
                      </SelectContent>
                    </Select>
                  </FormField>
                  <FormField label="Project boundary" name="token-project">
                    <Select
                      items={[
                        { label: "All projects", value: allProjects },
                        ...projects.map((project) => ({
                          label: project.name,
                          value: project.id,
                        })),
                      ]}
                      onValueChange={(value) =>
                        setProjectID(value === allProjects ? "" : String(value))
                      }
                      value={projectID || allProjects}
                    >
                      <SelectTrigger
                        className="h-8 w-full text-xs"
                        id="token-project"
                      >
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent align="start">
                        <SelectItem value={allProjects}>
                          All projects
                        </SelectItem>
                        {projects.map((project) => (
                          <SelectItem key={project.id} value={project.id}>
                            {project.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </FormField>
                </div>

                {role === "admin" && !projectID ? (
                  <div className="flex items-center gap-2 border-t border-amber-500/30 bg-amber-500/5 px-5 py-2.5 text-[10px] text-amber-800">
                    <ShieldAlert className="size-3.5" />
                    Unbound admin tokens are full root credentials and can
                    execute host commands.
                  </div>
                ) : null}

                <footer className="flex items-center justify-end gap-2 border-t border-border px-5 py-4">
                  <Dialog.Close
                    disabled={creating}
                    render={
                      <Button type="button" variant="ghost">
                        Cancel
                      </Button>
                    }
                  />
                  <Button
                    disabled={creating || name.trim() === ""}
                    form="api-token-create-form"
                    type="submit"
                  >
                    {creating ? (
                      <LoaderCircle className="animate-spin" />
                    ) : (
                      <KeyRound />
                    )}
                    {creating ? "Creating…" : "Create token"}
                  </Button>
                </footer>
              </form>
            )}

            {error ? (
              <p
                aria-live="polite"
                className="border-t border-destructive/20 bg-destructive/5 px-5 py-3 text-[10px] text-destructive"
              >
                {error}
              </p>
            ) : null}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
};
