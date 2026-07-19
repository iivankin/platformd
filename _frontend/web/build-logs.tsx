import { FileClock, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";

import { fetchBuildLog } from "@/api";

export const BuildLogs = ({
  deploymentID,
  projectID,
  running,
  serviceID,
}: {
  deploymentID: string;
  projectID: string;
  running: boolean;
  serviceID: string;
}) => {
  const [text, setText] = useState("");
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        setText(
          await fetchBuildLog(
            projectID,
            serviceID,
            deploymentID,
            controller.signal
          )
        );
        setError(undefined);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load build log"
        );
      } finally {
        if (running && !controller.signal.aborted) {
          timer = setTimeout(() => void load(), 1000);
        }
      }
    };
    void load();
    return () => {
      controller.abort();
      if (timer) {
        clearTimeout(timer);
      }
    };
  }, [deploymentID, projectID, running, serviceID]);

  if (error) {
    return (
      <p className="border-b border-destructive/30 bg-destructive/5 px-5 py-4 text-[10px] text-destructive">
        {error}
      </p>
    );
  }
  if (!text) {
    return (
      <div className="grid min-h-72 place-items-center px-8 text-center">
        <div>
          {running ? (
            <LoaderCircle className="mx-auto size-5 animate-spin text-muted-foreground" />
          ) : (
            <FileClock className="mx-auto size-5 text-muted-foreground" />
          )}
          <p className="mt-3 text-[10px] font-medium">
            {running ? "Waiting for build output" : "No build output"}
          </p>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Pull resolution and Dockerfile build output are stored per
            deployment.
          </p>
        </div>
      </div>
    );
  }
  return (
    <pre className="min-h-full overflow-auto p-5 font-mono text-[10px] leading-5 break-words whitespace-pre-wrap text-foreground">
      {text}
    </pre>
  );
};
