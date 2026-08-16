import { useQueryStates } from "nuqs";

import { errorDetailQueryParsers } from "@/telemetry-query-state";

import { configureApi } from "./api";
import { ErrorsConsole } from "./errors-console";
import type { App, DetailTarget } from "./types";

export const ErrorsApp = ({
  apiBasePath,
  app,
  onOpenTrace,
}: {
  apiBasePath: string;
  app: App;
  onOpenTrace?: (traceId: string) => void;
}) => {
  configureApi({ basePath: apiBasePath });
  const [state, setState] = useQueryStates(errorDetailQueryParsers);
  let initialDetail: DetailTarget | undefined;
  if (state.errorEvent) {
    initialDetail = { id: state.errorEvent, kind: "event" };
  } else if (state.errorIssue) {
    initialDetail = { id: state.errorIssue, kind: "issue" };
  }
  return (
    <ErrorsConsole
      app={app}
      initialDetail={initialDetail}
      key={`${app.id}:${initialDetail?.kind ?? "list"}:${initialDetail?.id ?? ""}`}
      onInitialDetailClosed={() =>
        void setState(
          { errorEvent: null, errorIssue: null },
          { history: "push" }
        )
      }
      onOpenTrace={onOpenTrace}
    />
  );
};
