import { Check, Copy, HardDrive, X } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { createObjectStore } from "@/api";
import type { ObjectStore } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

interface ObjectStoreCreatePanelProperties {
  onClose: () => void;
  onCreated: () => void;
  projectID: string;
}

export const ObjectStoreCreatePanel = ({
  onClose,
  onCreated,
  projectID,
}: ObjectStoreCreatePanelProperties) => {
  const [name, setName] = useState("");
  const [bucketName, setBucketName] = useState("");
  const [publicHostname, setPublicHostname] = useState("");
  const [corsOrigins, setCORSOrigins] = useState("");
  const [saving, setSaving] = useState(false);
  const [created, setCreated] = useState<ObjectStore | null>(null);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (saving) {
      return;
    }
    setSaving(true);
    setError(null);
    try {
      setCreated(
        await createObjectStore(projectID, {
          bucketName,
          corsOrigins: corsOrigins
            .split(/[\n,]/u)
            .map((origin) => origin.trim())
            .filter(Boolean),
          name,
          publicHostname: publicHostname || undefined,
        })
      );
    } catch (createError) {
      setError(
        createError instanceof Error
          ? createError.message
          : "Unable to create object storage"
      );
    } finally {
      setSaving(false);
    }
  };

  const credentials = created
    ? JSON.stringify(
        {
          accessKeyId: created.accessKey,
          bucket: created.bucketName,
          endpoint: created.publicHostname
            ? `https://${created.publicHostname}`
            : `http://${created.internalHostname}:9000`,
          region: created.region,
          secretAccessKey: created.secret,
        },
        null,
        2
      )
    : "";

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-[-8px_0_24px_oklch(0_0_0/5%)]">
      <div className="flex h-12 items-center border-b border-border px-4">
        <HardDrive className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">New object storage</h2>
        <Button
          aria-label="Close object storage form"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>
      {created ? (
        <div className="px-4 py-5">
          <div className="flex items-center gap-2 text-xs font-medium">
            <Check className="size-4 text-emerald-500" />
            Object storage is ready
          </div>
          <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
            Save the generated S3 credential now. Its secret is never returned
            again. Path-style requests use the fixed {created.region} region.
          </p>
          <pre className="mt-5 overflow-x-auto bg-muted px-3 py-3 font-mono text-[10px] leading-5 select-all">
            {credentials}
          </pre>
          <Button
            className="mt-3 w-full"
            onClick={() => {
              void navigator.clipboard.writeText(credentials);
              setCopied(true);
            }}
            variant="outline"
          >
            {copied ? <Check /> : <Copy />}
            {copied ? "Copied" : "Copy S3 configuration"}
          </Button>
          <Button className="mt-3 w-full" onClick={onCreated}>
            I saved the credentials
          </Button>
        </div>
      ) : (
        <form className="px-4 py-5" onSubmit={submit}>
          <FormField label="Resource name" name="object-store-name">
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="object-store-name"
              onChange={(event) => setName(event.target.value)}
              placeholder="assets"
              required
              spellCheck={false}
              value={name}
            />
          </FormField>
          <FormField label="Immutable bucket name" name="object-store-bucket">
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="object-store-bucket"
              onChange={(event) => setBucketName(event.target.value)}
              placeholder="shop-assets"
              required
              spellCheck={false}
              value={bucketName}
            />
          </FormField>
          <FormField
            label="Public hostname · optional"
            name="object-store-host"
          >
            <Input
              autoCapitalize="none"
              autoComplete="off"
              id="object-store-host"
              onChange={(event) => setPublicHostname(event.target.value)}
              placeholder="objects.example.com"
              spellCheck={false}
              value={publicHostname}
            />
            <p className="mt-1.5 text-[9px] leading-4 text-muted-foreground">
              Leave empty for project-network access only.
            </p>
          </FormField>
          <FormField label="CORS origins · optional" name="object-store-cors">
            <textarea
              className="min-h-20 w-full resize-y border border-input bg-background px-2.5 py-2 text-xs leading-5 outline-none placeholder:text-muted-foreground/70 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring"
              id="object-store-cors"
              onChange={(event) => setCORSOrigins(event.target.value)}
              placeholder={"https://app.example.com\nhttps://admin.example.com"}
              spellCheck={false}
              value={corsOrigins}
            />
          </FormField>
          {error ? (
            <p aria-live="polite" className="mt-4 text-[10px] text-destructive">
              {error}
            </p>
          ) : null}
          <div className="mt-5 flex justify-end gap-2 border-t border-border pt-4">
            <Button onClick={onClose} type="button" variant="ghost">
              Cancel
            </Button>
            <Button disabled={saving} type="submit">
              {saving ? "Creating…" : "Create storage"}
            </Button>
          </div>
        </form>
      )}
    </aside>
  );
};
