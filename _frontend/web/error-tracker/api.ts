import type {
  ApiToken,
  App,
  CreatedApiToken,
  CreatedApp,
  CreatedWebhook,
  EventDetail,
  Issue,
  IssueDetail,
  ListResponse,
  ReplayDetail,
  ReplayRecording,
  StoredDocument,
  Tracker,
  UploadToken,
  WebhookEvent,
} from "./types";

const storageKey = "error-tracker-admin-token";
const unauthorizedListeners = new Set<() => void>();
let adminToken = sessionStorage.getItem(storageKey) ?? "";
let adminAuthentication = true;
let apiBasePath = "";

export const configureApi = ({
  adminAuthentication: nextAdminAuthentication,
  basePath,
}: {
  adminAuthentication: boolean;
  basePath: string;
}) => {
  adminAuthentication = nextAdminAuthentication;
  apiBasePath = basePath.replace(/\/$/u, "");
};

const endpoint = (path: string) => `${apiBasePath}${path}`;

export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

export const hasAdminToken = () =>
  !adminAuthentication || adminToken.length > 0;

export const setAdminToken = (value: string) => {
  adminToken = value;
  if (value) {
    sessionStorage.setItem(storageKey, value);
  } else {
    sessionStorage.removeItem(storageKey);
  }
};

export const subscribeUnauthorized = (listener: () => void) => {
  unauthorizedListeners.add(listener);
  return () => {
    unauthorizedListeners.delete(listener);
  };
};

const readError = async (response: Response) => {
  const payload: unknown = await response.json().catch(() => null);
  if (payload && typeof payload === "object") {
    const record = payload as Record<string, unknown>;
    const message = record.message ?? record.error;
    if (typeof message === "string") {
      return message;
    }
  }
  return `Request failed with HTTP ${response.status}`;
};

const request = async <T>(path: string, init: RequestInit = {}) => {
  const headers = new Headers(init.headers);
  if (adminAuthentication && adminToken) {
    headers.set("authorization", `Bearer ${adminToken}`);
  }
  if (init.body && !headers.has("content-type")) {
    headers.set("content-type", "application/json");
  }
  const response = await fetch(endpoint(path), { ...init, headers });
  if (response.status === 401) {
    setAdminToken("");
    for (const listener of unauthorizedListeners) {
      listener();
    }
  }
  if (!response.ok) {
    throw new ApiError(await readError(response), response.status);
  }
  if (response.status === 204) {
    return null as T;
  }
  return (await response.json()) as T;
};

const json = (value: unknown): BodyInit => JSON.stringify(value);

export const api = {
  apps: () => request<App[]>("/api/v1/apps"),
  artifacts: (appId: string, query = "") =>
    request<ListResponse<StoredDocument>>(
      `/api/v1/apps/${appId}/artifacts?limit=100&query=${encodeURIComponent(query)}`
    ),
  createApp: (name: string, slug: string) =>
    request<CreatedApp>("/api/v1/apps", {
      body: json({ name, slug }),
      method: "POST",
    }),
  createToken: (name: string, role: ApiToken["role"], appId: string) =>
    request<CreatedApiToken>("/api/v1/tokens", {
      body: json({ appId: appId || null, name, role }),
      method: "POST",
    }),
  createWebhook: (appId: string, url: string, events: WebhookEvent[]) =>
    request<CreatedWebhook>(`/api/v1/apps/${appId}/webhooks`, {
      body: json({ events, url }),
      method: "POST",
    }),
  deleteWebhook: (appId: string, webhookId: string) =>
    request<null>(`/api/v1/apps/${appId}/webhooks/${webhookId}`, {
      method: "DELETE",
    }),
  event: (appId: string, eventId: string) =>
    request<EventDetail>(`/api/v1/apps/${appId}/events/${eventId}`),
  events: (appId: string, query = "") =>
    request<ListResponse<StoredDocument>>(
      `/api/v1/apps/${appId}/events?limit=100&query=${encodeURIComponent(query)}`
    ),
  issue: (appId: string, issueId: string) =>
    request<IssueDetail>(`/api/v1/apps/${appId}/issues/${issueId}`),
  issues: (appId: string, query = "") =>
    request<ListResponse<Issue>>(
      `/api/v1/apps/${appId}/issues?limit=100&query=${encodeURIComponent(query)}`
    ),
  replay: (appId: string, replayId: string) =>
    request<ReplayDetail>(`/api/v1/apps/${appId}/replays/${replayId}`),
  replayRecording: (appId: string, replayId: string) =>
    request<ReplayRecording>(
      `/api/v1/apps/${appId}/replays/${replayId}/recording`
    ),
  replays: (appId: string) =>
    request<ListResponse<StoredDocument>>(
      `/api/v1/apps/${appId}/replays?limit=100`
    ),
  revokeToken: (tokenId: string) =>
    request<null>(`/api/v1/tokens/${tokenId}`, { method: "DELETE" }),
  rotateUploadToken: (appId: string) =>
    request<UploadToken>(`/api/v1/apps/${appId}/upload-token`, {
      method: "POST",
    }),
  tokens: () => request<ApiToken[]>("/api/v1/tokens"),
  tracker: () => request<Tracker>("/api/v1/tracker"),
  updateIssue: (appId: string, issueId: string, status: Issue["status"]) =>
    request<Issue>(`/api/v1/apps/${appId}/issues/${issueId}`, {
      body: json({ status }),
      method: "PATCH",
    }),
};

export const downloadContent = async (appId: string, contentId: string) => {
  const headers = new Headers();
  if (adminAuthentication && adminToken) {
    headers.set("authorization", `Bearer ${adminToken}`);
  }
  const response = await fetch(
    endpoint(
      `/api/v1/apps/${encodeURIComponent(appId)}/content/${encodeURIComponent(contentId)}`
    ),
    { headers }
  );
  if (response.status === 401) {
    setAdminToken("");
    for (const listener of unauthorizedListeners) {
      listener();
    }
  }
  if (!response.ok) {
    throw new ApiError(
      `Download failed with HTTP ${response.status}`,
      response.status
    );
  }
  return response.blob();
};
