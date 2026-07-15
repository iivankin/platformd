import { useCallback, useEffect, useState } from "react";

import { fetchRegistryRepositories, fetchRegistrySettings } from "@/api";
import type { RegistryRepository } from "@/api";

const errorText = (error: unknown) =>
  error instanceof Error ? error.message : "Unable to load repository";

export const useRegistryRepository = (repositoryID: string) => {
  const [repository, setRepository] = useState<RegistryRepository>();
  const [hostname, setHostname] = useState("");
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [refreshVersion, setRefreshVersion] = useState(0);

  const refresh = useCallback(() => {
    setRefreshVersion((value) => value + 1);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const [settings, repositories] = await Promise.all([
          fetchRegistrySettings(controller.signal),
          fetchRegistryRepositories(controller.signal),
        ]);
        setHostname(settings.hostname);
        setRepository(
          repositories.find((candidate) => candidate.id === repositoryID)
        );
        setError(undefined);
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError));
        }
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [refreshVersion, repositoryID]);

  return { error, hostname, loading, refresh, repository };
};
