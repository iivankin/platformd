import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
} from "react";
import type { ReactNode } from "react";

import type { PendingResourceCreation } from "@/pending-resource-creation";
import type { PendingServiceSettings } from "@/service-settings-model";

type ProjectServiceChanges = Record<string, PendingServiceSettings>;
type AllProjectChanges = Record<string, ProjectServiceChanges>;
type ProjectResourceDrafts = Record<string, PendingResourceCreation>;
type AllProjectResourceDrafts = Record<string, ProjectResourceDrafts>;

interface ProjectChangesContextValue {
  changes: AllProjectChanges;
  resourceDrafts: AllProjectResourceDrafts;
  setServiceChange: (
    projectID: string,
    serviceID: string,
    change?: PendingServiceSettings
  ) => void;
  setResourceDraft: (
    projectID: string,
    draftID: string,
    draft?: PendingResourceCreation
  ) => void;
}

const ProjectChangesContext = createContext<ProjectChangesContextValue | null>(
  null
);
const noServiceChanges: ProjectServiceChanges = {};
const noResourceDrafts: ProjectResourceDrafts = {};

export const ProjectChangesProvider = ({
  children,
}: {
  children: ReactNode;
}) => {
  const [changes, setChanges] = useState<AllProjectChanges>({});
  const [resourceDrafts, setResourceDrafts] =
    useState<AllProjectResourceDrafts>({});
  const setServiceChange = useCallback(
    (projectID: string, serviceID: string, change?: PendingServiceSettings) => {
      setChanges((current) => {
        const project = { ...current[projectID] };
        const nextProject = change
          ? { ...project, [serviceID]: change }
          : Object.fromEntries(
              Object.entries(project).filter(
                ([candidateID]) => candidateID !== serviceID
              )
            );
        if (Object.keys(nextProject).length === 0) {
          return Object.fromEntries(
            Object.entries(current).filter(
              ([candidateID]) => candidateID !== projectID
            )
          );
        }
        return { ...current, [projectID]: nextProject };
      });
    },
    []
  );
  const setResourceDraft = useCallback(
    (projectID: string, draftID: string, draft?: PendingResourceCreation) => {
      setResourceDrafts((current) => {
        const project = { ...current[projectID] };
        const nextProject = draft
          ? { ...project, [draftID]: draft }
          : Object.fromEntries(
              Object.entries(project).filter(
                ([candidateID]) => candidateID !== draftID
              )
            );
        if (Object.keys(nextProject).length === 0) {
          return Object.fromEntries(
            Object.entries(current).filter(
              ([candidateID]) => candidateID !== projectID
            )
          );
        }
        return { ...current, [projectID]: nextProject };
      });
    },
    []
  );
  const value = useMemo(
    () => ({ changes, resourceDrafts, setResourceDraft, setServiceChange }),
    [changes, resourceDrafts, setResourceDraft, setServiceChange]
  );
  return (
    <ProjectChangesContext.Provider value={value}>
      {children}
    </ProjectChangesContext.Provider>
  );
};

export const useProjectChanges = (projectID: string) => {
  const context = useContext(ProjectChangesContext);
  if (!context) {
    throw new Error(
      "useProjectChanges must be used inside ProjectChangesProvider"
    );
  }
  return {
    resourceDrafts: context.resourceDrafts[projectID] ?? noResourceDrafts,
    serviceChanges: context.changes[projectID] ?? noServiceChanges,
    setResourceDraft: (draftID: string, draft?: PendingResourceCreation) =>
      context.setResourceDraft(projectID, draftID, draft),
    setServiceChange: (serviceID: string, change?: PendingServiceSettings) =>
      context.setServiceChange(projectID, serviceID, change),
  };
};
