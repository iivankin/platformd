import { HardDrive, X } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import type { CreateObjectStoreInput } from "@/api";
import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

type ObjectStoreDraftInput = Omit<
  CreateObjectStoreInput,
  "backupPolicy" | "credentials"
>;

interface ObjectStoreCreatePanelProperties {
  initialDraft?: ObjectStoreDraftInput;
  onClose: () => void;
  onDrafted: (input: ObjectStoreDraftInput) => void;
}

export const ObjectStoreCreatePanel = ({
  initialDraft,
  onClose,
  onDrafted,
}: ObjectStoreCreatePanelProperties) => {
  const [name, setName] = useState(initialDraft?.name ?? "");
  const [bucketName, setBucketName] = useState(initialDraft?.bucketName ?? "");
  const [publicHostname, setPublicHostname] = useState(
    initialDraft?.publicHostname ?? ""
  );
  const [corsOrigins, setCORSOrigins] = useState(
    initialDraft?.corsOrigins.join("\n") ?? ""
  );

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onDrafted({
      bucketName,
      corsOrigins: corsOrigins
        .split(/[\n,]/u)
        .map((origin) => origin.trim())
        .filter(Boolean),
      name,
      publicHostname: publicHostname || undefined,
    });
  };

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-lg">
      <div className="flex h-12 items-center border-b border-border px-4">
        <HardDrive className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">
          {initialDraft ? "Object storage draft" : "New object storage"}
        </h2>
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
        <FormField label="Public hostname · optional" name="object-store-host">
          <CertificateHostnameCombobox
            ariaLabel="Object storage public hostname"
            id="object-store-host"
            onChange={setPublicHostname}
            placeholder="objects.example.com"
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
        <div className="mt-5 flex justify-end gap-2 border-t border-border pt-4">
          <Button onClick={onClose} type="button" variant="ghost">
            Cancel
          </Button>
          <Button type="submit">
            {initialDraft ? "Update draft" : "Add storage draft"}
          </Button>
        </div>
      </form>
    </aside>
  );
};
