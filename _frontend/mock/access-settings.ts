import { json, noContent, readObject, stringField } from "./http";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

const settingsResponse = (state: MockState) => json(state.settings);

const handleTokens = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, tokenID, ...rest] = segments;
  if (root !== "tokens" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !tokenID) {
    return json({ tokens: state.tokens });
  }
  if (request.method === "DELETE" && tokenID) {
    state.tokens = state.tokens.map((token) =>
      token.id === tokenID ? { ...token, revokedAt: mockNow() } : token
    );
    return noContent();
  }
  if (request.method !== "POST" || tokenID) {
    return undefined;
  }
  const input = await readObject(request);
  const role: "admin" | "read" =
    stringField(input, "role") === "admin" ? "admin" : "read";
  const projectId = stringField(input, "projectId");
  const token = {
    createdAt: mockNow(),
    id: nextMockID(state, "token"),
    name: stringField(input, "name", "mock-token"),
    ...(projectId ? { projectId } : {}),
    role,
    token: "mock-only-token-do-not-use",
  };
  state.tokens = [{ ...token, token: undefined }, ...state.tokens];
  return json(token);
};

const handleSettings = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, resource, certificateID, ...rest] = segments;
  if (root !== "settings" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !resource) {
    return settingsResponse(state);
  }
  if (request.method === "PUT" && resource === "automation-hostname") {
    const input = await readObject(request);
    state.settings.automationHostname = stringField(input, "hostname");
    return settingsResponse(state);
  }
  if (resource !== "origin-certificates") {
    return undefined;
  }
  if (request.method === "POST" && !certificateID) {
    state.settings.certificates = [
      ...state.settings.certificates,
      {
        createdAt: mockNow(),
        dnsNames: ["*.mock.local"],
        id: nextMockID(state, "certificate"),
      },
    ];
    return settingsResponse(state);
  }
  if (request.method === "PUT" && certificateID) {
    state.settings.certificates = state.settings.certificates.map(
      (certificate) =>
        certificate.id === certificateID
          ? { ...certificate, createdAt: mockNow() }
          : certificate
    );
    return settingsResponse(state);
  }
  if (request.method !== "DELETE" || !certificateID) {
    return undefined;
  }
  state.settings.certificates = state.settings.certificates.filter(
    (certificate) => certificate.id !== certificateID
  );
  return settingsResponse(state);
};

export const handleAccessSettingsAPI = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> =>
  (await handleTokens(request, state, segments)) ??
  (await handleSettings(request, state, segments));
