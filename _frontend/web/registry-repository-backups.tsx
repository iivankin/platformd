import type { RegistryRepository } from "@/api";
import { ResourceBackupPanel } from "@/resource-backup-panel";

export const RegistryRepositoryBackups = ({
  repository,
}: {
  repository: RegistryRepository;
}) => (
  <div>
    <section className="border-b border-border bg-muted/15 px-5 py-3">
      <h3 className="text-[10px] font-medium">Repository backups</h3>
      <p className="mt-1 text-[9px] text-muted-foreground">
        Schedule, create, and restore encrypted snapshots of this repository.
      </p>
    </section>
    <ResourceBackupPanel resourceID={repository.id} resourceKind="registry" />
  </div>
);
