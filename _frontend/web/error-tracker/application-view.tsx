import { ErrorView, LoadingView } from "./common-ui";
import {
  ArtifactsView,
  EventsView,
  IssuesView,
  ReplaysView,
} from "./data-views";
import { ApplicationSettingsView } from "./settings-view";
import type {
  App,
  DetailTarget,
  Issue,
  ListResponse,
  StoredDocument,
  Tracker,
  ViewName,
} from "./types";

export type ViewData =
  | { kind: "artifacts"; payload: ListResponse<StoredDocument> }
  | { kind: "events"; payload: ListResponse<StoredDocument> }
  | { kind: "issues"; payload: ListResponse<Issue> }
  | { kind: "replays"; payload: ListResponse<StoredDocument> };

export const ApplicationView = ({
  app,
  error,
  loading,
  notify,
  openDetail,
  refresh,
  tracker,
  view,
  viewData,
}: {
  app: App;
  error: string;
  loading: boolean;
  notify: (message: string) => void;
  openDetail: (target: DetailTarget) => void;
  refresh: () => Promise<void>;
  tracker: Tracker;
  view: ViewName;
  viewData?: ViewData;
}) => {
  if (view === "settings") {
    return (
      <ApplicationSettingsView
        app={app}
        key={app.id}
        notify={notify}
        refresh={refresh}
        tracker={tracker}
      />
    );
  }
  if (loading && !viewData) {
    return <LoadingView />;
  }
  if (error) {
    return <ErrorView message={error} />;
  }
  if (viewData?.kind === "issues") {
    return <IssuesView items={viewData.payload.data} openDetail={openDetail} />;
  }
  if (viewData?.kind === "events") {
    return <EventsView items={viewData.payload.data} openDetail={openDetail} />;
  }
  if (viewData?.kind === "replays") {
    return (
      <ReplaysView items={viewData.payload.data} openDetail={openDetail} />
    );
  }
  if (viewData?.kind === "artifacts") {
    return <ArtifactsView items={viewData.payload.data} />;
  }
  return <LoadingView />;
};
