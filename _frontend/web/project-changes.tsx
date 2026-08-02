import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import type { ReactNode } from "react";

import type { PendingResourceCreation } from "@/pending-resource-creation";
import {
  loadStoredProjectChanges,
  saveStoredProjectChanges,
} from "@/project-changes-storage";
import type {
  AllProjectChanges,
  AllProjectResourceDrafts,
  ProjectResourceDrafts,
  ProjectServiceChanges,
} from "@/project-changes-storage";
import type { PendingServiceSettings } from "@/service-settings-model";

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
  const [stored, setStored] = useState(loadStoredProjectChanges);
  const { changes, resourceDrafts } = stored;
  useEffect(() => saveStoredProjectChanges(stored), [stored]);
  const setServiceChange = useCallback(
    (projectID: string, serviceID: string, change?: PendingServiceSettings) => {
      setStored((current) => {
        const project = { ...current.changes[projectID] };
        const nextProject = change
          ? { ...project, [serviceID]: change }
          : Object.fromEntries(
              Object.entries(project).filter(
                ([candidateID]) => candidateID !== serviceID
              )
            );
        if (Object.keys(nextProject).length === 0) {
          return {
            ...current,
            changes: Object.fromEntries(
              Object.entries(current.changes).filter(
                ([candidateID]) => candidateID !== projectID
              )
            ),
          };
        }
        return {
          ...current,
          changes: { ...current.changes, [projectID]: nextProject },
        };
      });
    },
    []
  );
  const setResourceDraft = useCallback(
    (projectID: string, draftID: string, draft?: PendingResourceCreation) => {
      setStored((current) => {
        const project = { ...current.resourceDrafts[projectID] };
        const nextProject = draft
          ? { ...project, [draftID]: draft }
          : Object.fromEntries(
              Object.entries(project).filter(
                ([candidateID]) => candidateID !== draftID
              )
            );
        if (Object.keys(nextProject).length === 0) {
          return {
            ...current,
            resourceDrafts: Object.fromEntries(
              Object.entries(current.resourceDrafts).filter(
                ([candidateID]) => candidateID !== projectID
              )
            ),
          };
        }
        return {
          ...current,
          resourceDrafts: {
            ...current.resourceDrafts,
            [projectID]: nextProject,
          },
        };
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
