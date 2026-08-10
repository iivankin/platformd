import { Plus } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

import { api } from "./api";
import { FormLabel } from "./common-ui";
import { FormFooter, Modal, SecretReveal } from "./dialog-frame";
import { errorMessage } from "./format";
import type { CreatedApp } from "./types";

export const CreateAppDialog = ({
  compact = false,
  onCreated,
}: {
  compact?: boolean;
  onCreated: (app: CreatedApp) => Promise<void>;
}) => {
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [created, setCreated] = useState<CreatedApp>();

  const reset = () => {
    setName("");
    setSlug("");
    setError("");
    setCreated(undefined);
  };
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPending(true);
    setError("");
    try {
      const next = await api.createApp(name.trim(), slug.trim());
      setCreated(next);
      await onCreated(next);
    } catch (createError) {
      setError(errorMessage(createError, "Unable to create application"));
    } finally {
      setPending(false);
    }
  };

  return (
    <>
      <Button
        aria-label={compact ? "Create application" : undefined}
        onClick={() => setOpen(true)}
        size={compact ? "icon" : "default"}
        variant={compact ? "ghost" : "default"}
      >
        <Plus />
        {compact ? null : "Create application"}
      </Button>
      <Modal
        description="The slug is used by sentry-cli and must be unique in this tracker."
        onOpenChange={(nextOpen) => {
          setOpen(nextOpen);
          if (!nextOpen) {
            reset();
          }
        }}
        open={open}
        title="Create application"
      >
        {created ? (
          <SecretReveal
            values={[
              { label: "Artifact upload token", value: created.authToken },
              { label: "DSN", value: created.dsn },
            ]}
          />
        ) : (
          <form onSubmit={submit}>
            <div className="grid gap-4 px-5 py-5 sm:grid-cols-2">
              <div>
                <FormLabel htmlFor="app-name">Name</FormLabel>
                <Input
                  autoFocus
                  id="app-name"
                  maxLength={80}
                  onChange={(event) => setName(event.target.value)}
                  placeholder="Storefront"
                  required
                  value={name}
                />
              </div>
              <div>
                <FormLabel htmlFor="app-slug">Slug</FormLabel>
                <Input
                  id="app-slug"
                  maxLength={48}
                  onChange={(event) => setSlug(event.target.value)}
                  pattern="[a-z0-9][a-z0-9-]*"
                  placeholder="storefront"
                  required
                  value={slug}
                />
              </div>
            </div>
            <FormFooter
              error={error}
              label="Create application"
              pending={pending}
            />
          </form>
        )}
      </Modal>
    </>
  );
};
