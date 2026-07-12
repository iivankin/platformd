import { useCallback, useEffect, useState } from "react";

import { fetchIdentity, fetchMeta, fetchProjects } from "@/api";
import type { Identity, Meta, Project } from "@/api";

const errorMessage = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

export const useAppData = () => {
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [identityError, setIdentityError] = useState<string | null>(null);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [metaError, setMetaError] = useState<string | null>(null);
  const [projects, setProjects] = useState<Project[]>([]);
  const [projectsError, setProjectsError] = useState<string | null>(null);
  const [projectsLoading, setProjectsLoading] = useState(true);

  useEffect(() => {
    const controller = new AbortController();
    const ignoreAbort = (error: unknown) =>
      error instanceof DOMException && error.name === "AbortError";

    const loadMeta = async () => {
      try {
        setMeta(await fetchMeta(controller.signal));
        setMetaError(null);
      } catch (error) {
        if (!ignoreAbort(error)) {
          setMetaError(errorMessage(error, "Meta request failed"));
        }
      }
    };
    const loadIdentity = async () => {
      try {
        setIdentity(await fetchIdentity(controller.signal));
        setIdentityError(null);
      } catch (error) {
        if (!ignoreAbort(error)) {
          setIdentityError(errorMessage(error, "Identity request failed"));
        }
      }
    };
    const loadProjects = async () => {
      try {
        setProjects(await fetchProjects(controller.signal));
        setProjectsError(null);
      } catch (error) {
        if (!ignoreAbort(error)) {
          setProjectsError(errorMessage(error, "Unable to load projects"));
        }
      } finally {
        if (!controller.signal.aborted) {
          setProjectsLoading(false);
        }
      }
    };

    // All independent control-plane reads start together to avoid a request
    // waterfall while preserving a useful partial UI if one endpoint fails.
    void loadMeta();
    void loadIdentity();
    void loadProjects();
    return () => controller.abort();
  }, []);

  const handleProjectCreated = useCallback((project: Project) => {
    setProjects((current) =>
      [...current, project].toSorted((left, right) =>
        left.name.localeCompare(right.name)
      )
    );
  }, []);

  return {
    handleProjectCreated,
    identity,
    identityError,
    meta,
    metaError,
    projects,
    projectsError,
    projectsLoading,
  };
};
