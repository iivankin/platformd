import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router";

import type { Project } from "@/api";

const lastProjectStorageKey = "platformd-last-project-id";

const projectIDFromPath = (pathname: string) => {
  const match = /^\/projects\/(?<projectID>[^/]+)/u.exec(pathname);
  const projectID = match?.groups?.projectID;

  return projectID && projectID !== "new" ? projectID : null;
};

const readLastProjectID = () => {
  try {
    return localStorage.getItem(lastProjectStorageKey);
  } catch {
    // Storage may be unavailable in hardened browser contexts.
    return null;
  }
};

const writeLastProjectID = (projectID: string) => {
  try {
    localStorage.setItem(lastProjectStorageKey, projectID);
  } catch {
    // Project navigation must keep working when storage is unavailable.
  }
};

const clearLastProjectID = () => {
  try {
    localStorage.removeItem(lastProjectStorageKey);
  } catch {
    // Project navigation must keep working when storage is unavailable.
  }
};

export const forgetLastProject = (projectID: string) => {
  if (readLastProjectID() === projectID) {
    clearLastProjectID();
  }
};

export const useLastProject = (
  projects: Project[],
  projectsLoading: boolean
) => {
  const location = useLocation();
  const navigate = useNavigate();

  useEffect(() => {
    const projectID = projectIDFromPath(location.pathname);
    if (!projectID || !projects.some((project) => project.id === projectID)) {
      return;
    }

    writeLastProjectID(projectID);
  }, [location.pathname, projects]);

  useEffect(() => {
    if (location.pathname !== "/" || projectsLoading) {
      return;
    }

    const projectID = readLastProjectID();
    if (!projectID) {
      return;
    }

    if (projects.some((project) => project.id === projectID)) {
      navigate(`/projects/${projectID}`, { replace: true });
      return;
    }

    clearLastProjectID();
  }, [location.pathname, navigate, projects, projectsLoading]);
};
