import { Route, Routes, useNavigate, useParams } from "react-router";

import type { Project } from "@/api";
import { ProjectAnalyticsPage } from "@/project-analytics-page";
import { ProjectCanvasPage } from "@/project-canvas-page";
import { useProjectChanges } from "@/project-changes";
import { ProjectPageTabs } from "@/project-page-tabs";
import { ProjectSettingsDialog } from "@/project-settings-dialog";
import { ProjectTelemetryPage } from "@/project-telemetry-page";
import { forgetLastProject } from "@/use-last-project";

export const ProjectPage = ({
  isDemo,
  onProjectDeleted,
  onProjectUpdated,
  projects,
}: {
  isDemo: boolean;
  onProjectDeleted: (projectID: string) => void;
  onProjectUpdated: (project: Project) => void;
  projects: Project[];
}) => {
  const { projectID = "" } = useParams();
  const navigate = useNavigate();
  const project = projects.find((item) => item.id === projectID);
  const { resourceDrafts, serviceChanges, setResourceDraft, setServiceChange } =
    useProjectChanges(projectID);

  const handleProjectDeleted = (deletedProjectID: string) => {
    for (const change of Object.values(serviceChanges)) {
      setServiceChange(change.serviceID);
    }
    for (const draft of Object.values(resourceDrafts)) {
      setResourceDraft(draft.id);
    }
    forgetLastProject(deletedProjectID);
    onProjectDeleted(deletedProjectID);
    void navigate("/projects", { replace: true });
  };

  return (
    <div className="flex h-full min-h-0 animate-in flex-col duration-200 fade-in slide-in-from-bottom-1">
      <header className="flex h-12 shrink-0 items-center gap-4 border-b border-border px-5">
        <div className="flex min-w-0 items-center gap-2.5">
          <p className="truncate text-xs font-medium">
            {project?.name ?? "Project"}
          </p>
          {project ? (
            <ProjectSettingsDialog
              onDeleted={handleProjectDeleted}
              onUpdated={onProjectUpdated}
              project={project}
            />
          ) : null}
        </div>
        <ProjectPageTabs projectID={projectID} />
      </header>
      <div className="min-h-0 flex-1">
        <Routes>
          <Route element={<ProjectAnalyticsPage />} path="analytics" />
          <Route element={<ProjectTelemetryPage />} path="telemetry" />
          <Route
            element={<ProjectCanvasPage isDemo={isDemo} />}
            path=":resourceCollection/:resourceID/:view?"
          />
          <Route element={<ProjectCanvasPage isDemo={isDemo} />} index />
        </Routes>
      </div>
    </div>
  );
};
