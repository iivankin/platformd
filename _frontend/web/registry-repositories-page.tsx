import { Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";

import { fetchRegistryRepositories } from "@/api";
import type { RegistryRepository } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { PageStack } from "@/components/ui/page-stack";
import { RegistryRepositoryCreateForm } from "@/registry-repository-create-form";
import { RegistryRepositoryList } from "@/registry-repository-list";

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export const RegistryRepositoriesPage = () => {
  const navigate = useNavigate();
  const [repositories, setRepositories] = useState<RegistryRepository[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        setRepositories(await fetchRegistryRepositories(controller.signal));
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load repositories"));
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoaded(true);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  return (
    <PageStack className="min-h-[36rem] flex-1">
      <SectionCard className="flex min-h-16 items-center justify-between gap-4 px-5 py-3">
        <div>
          <p className="text-xs font-medium">Image repositories</p>
          <p className="mt-1 text-[10px] text-muted-foreground">
            Store and control the images deployed by your services.
          </p>
        </div>
        <Button onClick={() => setCreating(true)} size="sm">
          <Plus /> New repository
        </Button>
      </SectionCard>

      {creating ? (
        <RegistryRepositoryCreateForm
          onCancel={() => setCreating(false)}
          onCreated={(created) => {
            setRepositories((current) => [created, ...current]);
            setCreating(false);
            void navigate(`/registry/repositories/${created.id}/access`);
          }}
          onError={setError}
        />
      ) : null}

      {error ? (
        <SectionCard className="bg-destructive/5 px-5 py-3 text-[10px] text-destructive ring-destructive/30">
          {error}
        </SectionCard>
      ) : null}

      {loaded ? (
        <RegistryRepositoryList
          onSelect={(repository) =>
            void navigate(`/registry/repositories/${repository.id}/images`)
          }
          repositories={repositories}
        />
      ) : (
        <SectionCard className="grid min-h-64 place-items-center text-[10px] text-muted-foreground">
          Loading repositories…
        </SectionCard>
      )}
    </PageStack>
  );
};
