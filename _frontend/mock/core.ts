import { handleAccessSettingsAPI } from "./access-settings";
import { handleBackupsAPI } from "./backups";
import { apiSegments } from "./http";
import type { MockState } from "./state";
import { handleSystemAPI } from "./system";

export const handleCoreAPI = async (
  request: Request,
  state: MockState,
  pathname: string,
  url: URL
): Promise<Response | undefined> => {
  const segments = apiSegments(pathname);
  return (
    (await handleBackupsAPI(request, state, segments)) ??
    (await handleAccessSettingsAPI(request, state, segments)) ??
    handleSystemAPI(request, state, segments, url)
  );
};
