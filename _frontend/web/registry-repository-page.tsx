import { PackageOpen } from "lucide-react";
import { Navigate, useNavigate, useParams } from "react-router";

import { RegistryRepositoryDetail } from "@/registry-repository-detail";
import type { RegistryRepositoryView } from "@/registry-repository-detail";
import { useRegistryRepository } from "@/use-registry-repository";

const isRepositoryView = (value?: string): value is RegistryRepositoryView =>
  value === "images" || value === "access" || value === "maintenance";

export const RegistryRepositoryPage = () => {
  const navigate = useNavigate();
  const { repositoryID = "", view } = useParams();
  const { error, hostname, loading, refresh, repository } =
    useRegistryRepository(repositoryID);

  if (!isRepositoryView(view)) {
    return <Navigate replace to="images" />;
  }
  if (loading) {
    return (
      <div className="grid min-h-72 place-items-center border-b border-border text-[10px] text-muted-foreground">
        Loading repository…
      </div>
    );
  }
  if (error || !repository) {
    return (
      <div className="grid min-h-72 place-items-center border-b border-border px-8 text-center">
        <div>
          <PackageOpen className="mx-auto size-6 text-muted-foreground" />
          <p className="mt-4 text-xs font-medium">Repository unavailable</p>
          <p className="mt-2 text-[10px] text-muted-foreground">
            {error ?? "The repository no longer exists."}
          </p>
        </div>
      </div>
    );
  }

  return (
    <RegistryRepositoryDetail
      hostname={hostname}
      onChanged={refresh}
      onDeleted={() => void navigate("/registry/repositories")}
      repository={repository}
      view={view}
    />
  );
};
