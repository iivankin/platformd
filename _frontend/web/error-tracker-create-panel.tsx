import { Bug, X } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import type { CreateErrorTrackerInput } from "@/api";
import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

type ErrorTrackerDraftInput = Omit<CreateErrorTrackerInput, "backupPolicy">;

export const ErrorTrackerCreatePanel = ({
  initialDraft,
  onClose,
  onDrafted,
}: {
  initialDraft?: ErrorTrackerDraftInput;
  onClose: () => void;
  onDrafted: (input: ErrorTrackerDraftInput) => void;
}) => {
  const [name, setName] = useState(initialDraft?.name ?? "");
  const [publicHostname, setPublicHostname] = useState(
    initialDraft?.publicHostname ?? ""
  );

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    onDrafted({
      name: name.trim(),
      publicHostname: publicHostname || undefined,
    });
  };

  return (
    <aside className="absolute inset-y-0 right-0 z-20 w-full max-w-md overflow-y-auto border-l border-border bg-background shadow-lg">
      <div className="flex h-12 items-center border-b border-border px-4">
        <Bug className="size-4 text-muted-foreground" />
        <h2 className="ml-2 text-xs font-medium">
          {initialDraft ? "Error tracker draft" : "New error tracker"}
        </h2>
        <Button
          aria-label="Close error tracker form"
          className="ml-auto"
          onClick={onClose}
          size="icon"
          variant="ghost"
        >
          <X />
        </Button>
      </div>
      <form className="px-4 py-5" onSubmit={submit}>
        <FormField label="Resource name" name="error-tracker-name">
          <Input
            autoCapitalize="none"
            autoComplete="off"
            id="error-tracker-name"
            onChange={(event) => setName(event.target.value)}
            placeholder="errors"
            required
            spellCheck={false}
            value={name}
          />
        </FormField>
        <FormField label="Public hostname · optional" name="error-tracker-host">
          <CertificateHostnameCombobox
            ariaLabel="Error tracker public hostname"
            id="error-tracker-host"
            onChange={setPublicHostname}
            placeholder="errors.example.com"
            value={publicHostname}
          />
          <p className="mt-1.5 text-[9px] leading-4 text-muted-foreground">
            Client DSNs use the internal project hostname until public access is
            enabled.
          </p>
        </FormField>
        <div className="mt-5 flex justify-end gap-2 border-t border-border pt-4">
          <Button onClick={onClose} type="button" variant="ghost">
            Cancel
          </Button>
          <Button type="submit">
            {initialDraft ? "Update draft" : "Add tracker draft"}
          </Button>
        </div>
      </form>
    </aside>
  );
};
