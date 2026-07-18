import { Check, LoaderCircle } from "lucide-react";
import type { FormEvent } from "react";

import type { SetBackupTargetInput } from "@/api";
import { Button } from "@/components/ui/button";
import { FormCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { FormField } from "@/form-field";

export const BackupStorageForm = ({
  busy,
  canSubmit,
  configured,
  input,
  onCancel,
  onSubmit,
  onUpdate,
}: {
  busy: boolean;
  canSubmit: boolean;
  configured: boolean;
  input: SetBackupTargetInput;
  onCancel: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onUpdate: (field: keyof SetBackupTargetInput, value: string) => void;
}) => (
  <FormCard onSubmit={onSubmit}>
    <div className="border-b border-border px-5 py-3 text-[10px] text-muted-foreground">
      Connect an S3-compatible bucket. The connection is checked before it is
      saved.
    </div>
    <div className="grid md:grid-cols-2 xl:grid-cols-3">
      <div className="border-b border-border px-5 pt-4 md:border-r">
        <FormField label="Storage name" name="backup-name">
          <Input
            autoFocus
            id="backup-name"
            onChange={(event) => onUpdate("name", event.target.value)}
            placeholder="Off-site · EU"
            value={input.name}
          />
        </FormField>
      </div>
      <div className="border-b border-border px-5 pt-4 md:border-r">
        <FormField label="Endpoint" name="backup-endpoint">
          <Input
            autoComplete="url"
            id="backup-endpoint"
            onChange={(event) => onUpdate("endpoint", event.target.value)}
            placeholder="https://s3.example.com"
            value={input.endpoint}
          />
        </FormField>
      </div>
      <div className="border-b border-border px-5 pt-4 xl:border-r">
        <FormField label="Region" name="backup-region">
          <Input
            id="backup-region"
            onChange={(event) => onUpdate("region", event.target.value)}
            placeholder="eu-central-003"
            value={input.region}
          />
        </FormField>
      </div>
      <div className="border-b border-border px-5 pt-4 md:border-r xl:border-r-0">
        <FormField label="Bucket" name="backup-bucket">
          <Input
            id="backup-bucket"
            onChange={(event) => onUpdate("bucket", event.target.value)}
            placeholder="platformd-backups"
            value={input.bucket}
          />
        </FormField>
      </div>
      <div className="border-b border-border px-5 pt-4 xl:border-r">
        <FormField label="Path prefix (optional)" name="backup-prefix">
          <Input
            id="backup-prefix"
            onChange={(event) => onUpdate("prefix", event.target.value)}
            placeholder="installation-a"
            value={input.prefix}
          />
        </FormField>
      </div>
      <div className="border-b border-border px-5 pt-4 md:border-r">
        <FormField label="Access key" name="backup-access-key">
          <Input
            autoComplete="off"
            id="backup-access-key"
            onChange={(event) => onUpdate("accessKeyId", event.target.value)}
            value={input.accessKeyId}
          />
        </FormField>
      </div>
      <div className="border-b border-border px-5 pt-4">
        <FormField label="Secret key" name="backup-secret-key">
          <Input
            autoComplete="new-password"
            id="backup-secret-key"
            onChange={(event) =>
              onUpdate("secretAccessKey", event.target.value)
            }
            placeholder={
              configured ? "Enter the secret again" : "Secret access key"
            }
            type="password"
            value={input.secretAccessKey}
          />
        </FormField>
      </div>
    </div>
    <div className="flex flex-wrap items-center gap-2 px-5 py-3">
      <p className="mr-auto text-[9px] text-muted-foreground">
        The connection is verified before saving.
      </p>
      <Button onClick={onCancel} size="sm" type="button" variant="ghost">
        Cancel
      </Button>
      <Button disabled={busy || !canSubmit} size="sm" type="submit">
        {busy ? <LoaderCircle className="animate-spin" /> : <Check />}
        {busy ? "Checking…" : "Connect storage"}
      </Button>
    </div>
  </FormCard>
);
