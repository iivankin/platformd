import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
} from "react";
import type { ReactNode } from "react";

import type { PendingServiceSettings } from "@/service-settings-model";

type ProjectServiceChanges = Record<string, PendingServiceSettings>;
type AllProjectChanges = Record<string, ProjectServiceChanges>;

interface ProjectChangesContextValue {
  changes: AllProjectChanges;
  setServiceChange: (
    projectID: string,
    serviceID: string,
    change?: PendingServiceSettings
  ) => void;
}

const ProjectChangesContext = createContext<ProjectChangesContextValue | null>(
  null
);
const noServiceChanges: ProjectServiceChanges = {};

export const ProjectChangesProvider = ({
  children,
}: {
  children: ReactNode;
}) => {
  const [changes, setChanges] = useState<AllProjectChanges>({});
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
  const value = useMemo(
    () => ({ changes, setServiceChange }),
    [changes, setServiceChange]
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
    serviceChanges: context.changes[projectID] ?? noServiceChanges,
    setServiceChange: (serviceID: string, change?: PendingServiceSettings) =>
      context.setServiceChange(projectID, serviceID, change),
  };
};
