import { LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";
import { useParams } from "react-router";

import { isAbortError } from "@/analytics-model";
import { fetchProjectCanvas } from "@/api";
import { ProjectAnalytics } from "@/project-analytics";

export const ProjectAnalyticsPage = () => {
  const { projectID = "" } = useParams();
  const [serviceIDs, setServiceIDs] = useState<string[]>();
  const [loadedProjectID, setLoadedProjectID] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const canvas = await fetchProjectCanvas(projectID, controller.signal);
        setServiceIDs(
          canvas.resources
            .filter((resource) => resource.kind === "service")
            .map((resource) => resource.id)
        );
        setLoadedProjectID(projectID);
        setError("");
      } catch (loadError) {
        if (!isAbortError(loadError)) {
          setError(
            loadError instanceof Error
              ? loadError.message
              : "Unable to load web analytics"
          );
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID]);

  const readyIDs = loadedProjectID === projectID ? serviceIDs : undefined;

  return (
    <div className="flex h-full min-h-0 flex-col">
      {error ? (
        <p className="shrink-0 border-b border-destructive/35 bg-destructive/5 px-5 py-3 text-[10px] text-destructive">
          {error}
        </p>
      ) : null}
      {readyIDs ? (
        <ProjectAnalytics
          key={projectID}
          projectID={projectID}
          serviceIDs={readyIDs}
        />
      ) : null}
      {readyIDs || error ? null : (
        <div className="grid min-h-0 flex-1 place-items-center text-[10px] text-muted-foreground">
          <span className="flex items-center gap-2">
            <LoaderCircle className="size-3 animate-spin" /> Opening web
            analytics
          </span>
        </div>
      )}
    </div>
  );
};
