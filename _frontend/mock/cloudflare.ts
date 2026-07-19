import { apiSegments, json, mockError, readObject, stringField } from "./http";
import type { MockState } from "./state";
import { mockNow } from "./state";

export const handleCloudflareAPI = async (
  request: Request,
  state: MockState,
  pathname: string
): Promise<Response | undefined> => {
  const segments = apiSegments(pathname);
  if (
    segments.length !== 2 ||
    segments[0] !== "settings" ||
    segments[1] !== "cloudflare"
  ) {
    return undefined;
  }
  if (request.method === "GET") {
    return json(state.cloudflareDNSSettings);
  }
  if (request.method === "PUT") {
    const token = stringField(await readObject(request), "apiToken");
    if (token.trim().length < 20) {
      return mockError(
        "cloudflare_dns_configure_failed",
        "A valid Cloudflare API Token is required."
      );
    }
    state.cloudflareDNSSettings = {
      configured: true,
      updatedAt: mockNow(),
    };
    return json(state.cloudflareDNSSettings);
  }
  return undefined;
};
