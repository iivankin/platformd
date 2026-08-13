import { configureApi } from "./api";
import { ErrorsConsole } from "./errors-console";
import type { App } from "./types";

export const ErrorsApp = ({
  apiBasePath,
  app,
}: {
  apiBasePath: string;
  app: App;
}) => {
  configureApi({ basePath: apiBasePath });
  return <ErrorsConsole app={app} />;
};
