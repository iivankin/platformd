import { Box, Database, HardDrive, Network } from "lucide-react";

import { CertificateHostnameCombobox } from "@/certificate-hostname-combobox";
import { SectionCard } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { PageStack } from "@/components/ui/page-stack";
import { ManagedImageTagCombobox } from "@/managed-image-tag-combobox";
import type { PendingResourceCreation } from "@/pending-resource-creation";

type PostgresDraft = Extract<PendingResourceCreation, { kind: "postgres" }>;
type RedisDraft = Extract<PendingResourceCreation, { kind: "redis" }>;
type StorageDraft = Extract<PendingResourceCreation, { kind: "storage" }>;
type ManagedDatabaseDraft = PostgresDraft | RedisDraft;
type ManagedDraft = Exclude<PendingResourceCreation, { kind: "service" }>;

const numberValue = (value: string) =>
  value === "" ? undefined : Number(value);

const ManagedDatabaseSettings = ({
  draft,
  internalHostname,
  onChange,
}: {
  draft: ManagedDatabaseDraft;
  internalHostname: string;
  onChange: (draft: ManagedDatabaseDraft) => void;
}) => {
  const isPostgres = draft.kind === "postgres";
  const Icon = isPostgres ? Database : Box;
  const engine = isPostgres ? "postgres" : "redis";

  const updateInput = (input: ManagedDatabaseDraft["input"]) =>
    onChange({ ...draft, input } as ManagedDatabaseDraft);

  return (
    <PageStack>
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <div className="flex items-center gap-2">
            <Icon className="size-3.5 text-muted-foreground" />
            <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
              {isPostgres ? "PostgreSQL" : "Redis"}
            </h3>
          </div>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Resource identity and official runtime image.
          </p>
        </div>
        <div className="grid border-t border-border md:grid-cols-2 lg:border-t-0 lg:border-l">
          <label
            className="border-b border-border px-5 py-4 md:border-r"
            htmlFor="draft-resource-name"
          >
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Resource name
            </span>
            <Input
              className="mt-2"
              id="draft-resource-name"
              onChange={(event) =>
                updateInput({ ...draft.input, name: event.target.value })
              }
              value={draft.input.name}
            />
          </label>
          <div className="border-b border-border px-5 py-4">
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Official image tag
            </span>
            <ManagedImageTagCombobox
              className="mt-2"
              engine={engine}
              id="draft-image-tag"
              onChange={(imageTag) => updateInput({ ...draft.input, imageTag })}
              value={draft.input.imageTag}
            />
          </div>
          <label className="px-5 py-4 md:border-r" htmlFor="draft-cpu">
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              CPU millicores
            </span>
            <Input
              className="mt-2"
              id="draft-cpu"
              min={1}
              onChange={(event) =>
                updateInput({
                  ...draft.input,
                  cpuMillicores: numberValue(event.target.value),
                })
              }
              placeholder="Unlimited"
              type="number"
              value={draft.input.cpuMillicores ?? ""}
            />
          </label>
          <label className="px-5 py-4" htmlFor="draft-memory">
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Memory MiB
            </span>
            <Input
              className="mt-2"
              id="draft-memory"
              min={1}
              onChange={(event) => {
                const value = numberValue(event.target.value);
                updateInput({
                  ...draft.input,
                  memoryBytes:
                    value === undefined ? undefined : value * 1024 * 1024,
                });
              }}
              placeholder="Unlimited"
              type="number"
              value={
                draft.input.memoryBytes
                  ? draft.input.memoryBytes / 1024 / 1024
                  : ""
              }
            />
          </label>
        </div>
      </SectionCard>

      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <div className="flex items-center gap-2">
            <Network className="size-3.5 text-muted-foreground" />
            <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
              Private network
            </h3>
          </div>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Other project resources can use this hostname after Deploy.
          </p>
        </div>
        <div className="border-t border-border px-5 py-4 lg:border-t-0 lg:border-l">
          <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            Internal hostname
          </p>
          <code className="mt-2 block text-[10px]">{internalHostname}</code>
        </div>
      </SectionCard>
    </PageStack>
  );
};

const StorageSettings = ({
  draft,
  internalHostname,
  onChange,
}: {
  draft: StorageDraft;
  internalHostname: string;
  onChange: (draft: StorageDraft) => void;
}) => {
  const updateInput = (input: StorageDraft["input"]) =>
    onChange({ ...draft, input });

  return (
    <PageStack>
      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <div className="flex items-center gap-2">
            <HardDrive className="size-3.5 text-muted-foreground" />
            <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
              Object storage
            </h3>
          </div>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Resource name and immutable S3 bucket identity.
          </p>
        </div>
        <div className="grid border-t border-border md:grid-cols-2 lg:border-t-0 lg:border-l">
          <label
            className="border-b border-border px-5 py-4 md:border-r"
            htmlFor="draft-storage-name"
          >
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Resource name
            </span>
            <Input
              className="mt-2"
              id="draft-storage-name"
              onChange={(event) =>
                updateInput({ ...draft.input, name: event.target.value })
              }
              value={draft.input.name}
            />
          </label>
          <label
            className="border-b border-border px-5 py-4"
            htmlFor="draft-bucket-name"
          >
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Bucket name
            </span>
            <Input
              className="mt-2"
              id="draft-bucket-name"
              onChange={(event) =>
                updateInput({ ...draft.input, bucketName: event.target.value })
              }
              value={draft.input.bucketName}
            />
          </label>
          <div className="border-b border-border px-5 py-4 md:col-span-2">
            <p className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Internal hostname
            </p>
            <code className="mt-2 block text-[10px]">{internalHostname}</code>
          </div>
        </div>
      </SectionCard>

      <SectionCard className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <div className="flex items-center gap-2">
            <Network className="size-3.5 text-muted-foreground" />
            <h3 className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
              Public access
            </h3>
          </div>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Optional HTTPS hostname and browser access origins.
          </p>
        </div>
        <div className="border-t border-border lg:border-t-0 lg:border-l">
          <div className="border-b border-border px-5 py-4">
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Public hostname
            </span>
            <div className="mt-2">
              <CertificateHostnameCombobox
                ariaLabel="Object storage public hostname"
                id="draft-storage-hostname"
                onChange={(publicHostname) =>
                  updateInput({
                    ...draft.input,
                    publicHostname: publicHostname || undefined,
                  })
                }
                placeholder="objects.example.com"
                value={draft.input.publicHostname ?? ""}
              />
            </div>
          </div>
          <label className="block px-5 py-4" htmlFor="draft-storage-cors">
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              CORS origins
            </span>
            <textarea
              className="mt-2 min-h-24 w-full resize-y border border-input bg-background px-2.5 py-2 text-xs leading-5 text-foreground outline-none placeholder:text-muted-foreground/70 focus-visible:border-foreground/40 focus-visible:ring-1 focus-visible:ring-ring"
              id="draft-storage-cors"
              onChange={(event) =>
                updateInput({
                  ...draft.input,
                  corsOrigins: event.target.value
                    .split(/[\n,]/u)
                    .map((origin) => origin.trim())
                    .filter(Boolean),
                })
              }
              placeholder={"https://app.example.com\nhttps://admin.example.com"}
              value={draft.input.corsOrigins.join("\n")}
            />
          </label>
        </div>
      </SectionCard>
    </PageStack>
  );
};

export const ResourceDraftSettings = ({
  draft,
  internalHostname,
  onChange,
}: {
  draft: ManagedDraft;
  internalHostname: string;
  onChange: (draft: ManagedDraft) => void;
}) => {
  if (draft.kind === "storage") {
    return (
      <StorageSettings
        draft={draft}
        internalHostname={internalHostname}
        onChange={onChange}
      />
    );
  }
  return (
    <ManagedDatabaseSettings
      draft={draft}
      internalHostname={internalHostname}
      onChange={onChange}
    />
  );
};
