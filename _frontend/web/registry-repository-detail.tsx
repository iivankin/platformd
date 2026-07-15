import { ArrowLeft, PackageOpen } from "lucide-react";
import { Link } from "react-router";

import type { RegistryRepository } from "@/api";
import { PageTabs } from "@/page-tabs";
import { RegistryRepositoryAccess } from "@/registry-repository-access";
import { RegistryRepositoryImages } from "@/registry-repository-images";
import { formatRegistryBytes } from "@/registry-repository-list";
import { RegistryRepositoryMaintenance } from "@/registry-repository-maintenance";

export type RegistryRepositoryView = "access" | "images" | "maintenance";

export const RegistryRepositoryDetail = ({
  hostname,
  onDeleted,
  onChanged,
  repository,
  view,
}: {
  hostname: string;
  onDeleted: (repositoryID: string) => void;
  onChanged: () => void;
  repository: RegistryRepository;
  view: RegistryRepositoryView;
}) => {
  const basePath = `/registry/repositories/${repository.id}`;
  const tabs = [
    { label: "Images", path: `${basePath}/images` },
    { label: "Access", path: `${basePath}/access` },
    { label: "Maintenance", path: `${basePath}/maintenance` },
  ];

  return (
    <div className="enter-row min-h-full">
      <section className="border-b border-border">
        <div className="flex min-h-20 items-center gap-4 px-5 py-4">
          <Link
            aria-label="Back to repositories"
            className="grid size-8 shrink-0 place-items-center border border-border text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
            to="/registry/repositories"
          >
            <ArrowLeft className="size-3.5" />
          </Link>
          <span className="grid size-9 shrink-0 place-items-center bg-muted/50">
            <PackageOpen className="size-4 text-muted-foreground" />
          </span>
          <div className="min-w-0">
            <p className="text-[8px] tracking-[0.14em] text-muted-foreground uppercase">
              Repository
            </p>
            <h2 className="mt-1 truncate text-sm font-medium">
              {repository.name}
            </h2>
            <p className="mt-1 truncate text-[9px] text-muted-foreground">
              {hostname
                ? `${hostname}/${repository.name}`
                : "Add a Registry address before pushing images"}
            </p>
          </div>
        </div>
        <dl className="grid border-t border-border bg-muted/15 text-[9px] sm:grid-cols-3">
          {[
            ["Manifests", repository.manifestCount.toString()],
            ["Blobs", repository.blobCount.toString()],
            ["Stored", formatRegistryBytes(repository.totalBlobBytes)],
          ].map(([label, value]) => (
            <div
              className="border-b border-border px-4 py-3 sm:border-r sm:border-b-0"
              key={label}
            >
              <dt className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                {label}
              </dt>
              <dd className="mt-1.5">{value}</dd>
            </div>
          ))}
        </dl>
      </section>
      <PageTabs label={`${repository.name} pages`} tabs={tabs} />

      {view === "images" ? (
        <RegistryRepositoryImages repository={repository} />
      ) : null}
      {view === "access" ? (
        <RegistryRepositoryAccess
          hostname={hostname}
          onChanged={onChanged}
          repository={repository}
        />
      ) : null}
      {view === "maintenance" ? (
        <RegistryRepositoryMaintenance
          onDeleted={onDeleted}
          onChanged={onChanged}
          repository={repository}
        />
      ) : null}
    </div>
  );
};
