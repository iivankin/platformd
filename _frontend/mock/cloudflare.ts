import { apiSegments, json, mockError, readObject, stringField } from "./http";
import type { MockState } from "./state";
import { mockNow } from "./state";

const handleCloudflareMeshAPI = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  if (segments.length === 2 && request.method === "GET") {
    return json(state.cloudflareMeshSettings);
  }
  if (
    segments.length === 3 &&
    segments[2] === "credential" &&
    request.method === "GET"
  ) {
    if (!state.cloudflareMeshCredential) {
      return mockError(
        "cloudflare_mesh_not_configured",
        "Cloudflare Mesh is not configured.",
        404
      );
    }
    return json(state.cloudflareMeshCredential);
  }
  if (segments.length === 2 && request.method === "PUT") {
    const body = await readObject(request);
    const accountId = stringField(body, "accountId").trim();
    const apiToken = stringField(body, "apiToken").trim();
    if (!/^[0-9a-f]{32}$/u.test(accountId) || apiToken.length < 20) {
      return mockError(
        "cloudflare_mesh_configure_failed",
        "A valid account ID and API token are required."
      );
    }
    state.cloudflareMeshCredential = { accountId, apiToken };
    state.cloudflareMeshSettings = {
      accountId,
      configured: true,
      interfaceName: "CloudflareWARP",
      meshIp: "100.96.0.21",
      nodeId: "mesh-node-mock",
      nodeName: "platformd-installation-mock",
      status: "connected",
      updatedAt: mockNow(),
    };
    return json(state.cloudflareMeshSettings);
  }
  if (
    segments.length === 3 &&
    segments[2] === "connect" &&
    request.method === "POST"
  ) {
    if (!state.cloudflareMeshCredential) {
      return mockError(
        "cloudflare_mesh_not_configured",
        "Cloudflare Mesh is not configured.",
        404
      );
    }
    state.cloudflareMeshSettings.status = "connected";
    return json(state.cloudflareMeshSettings);
  }
  return undefined;
};

export const handleCloudflareAPI = async (
  request: Request,
  state: MockState,
  pathname: string
): Promise<Response | undefined> => {
  const segments = apiSegments(pathname);
  if (segments[0] === "settings" && segments[1] === "cloudflare-mesh") {
    return handleCloudflareMeshAPI(request, state, segments);
  }
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
