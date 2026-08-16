import type {
  CreatedWebhook,
  EventDetail,
  Issue,
  IssueDetail,
  ListResponse,
  ReplayRecording,
  StoredDocument,
  UploadToken,
  WebhookEvent,
} from "./types";

let serviceBasePath = "";

export const configureApi = ({ basePath }: { basePath: string }) => {
  serviceBasePath = basePath.replace(/\/$/u, "");
};

const endpoint = (path: string) => `${serviceBasePath}${path}`;

export class ApiError extends Error {
  readonly status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

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
  if (init.body && !headers.has("content-type")) {
    headers.set("content-type", "application/json");
  }
  const response = await fetch(endpoint(path), { ...init, headers });
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
  artifacts: (_serviceId: string, query = "") =>
    request<ListResponse<StoredDocument>>(
      `/errors/artifacts?limit=100&query=${encodeURIComponent(query)}`
    ),
  createWebhook: (_serviceId: string, url: string, events: WebhookEvent[]) =>
    request<CreatedWebhook>("/telemetry/webhooks", {
      body: json({ events, url }),
      method: "POST",
    }),
  deleteWebhook: (_serviceId: string, webhookId: string) =>
    request<null>(`/telemetry/webhooks/${encodeURIComponent(webhookId)}`, {
      method: "DELETE",
    }),
  event: (_serviceId: string, eventId: string) =>
    request<EventDetail>(`/errors/events/${encodeURIComponent(eventId)}`),
  events: (_serviceId: string, query = "") =>
    request<ListResponse<StoredDocument>>(
      `/errors/events?limit=100&query=${encodeURIComponent(query)}`
    ),
  issue: (_serviceId: string, issueId: string, offset = 0) =>
    request<IssueDetail>(
      `/errors/issues/${encodeURIComponent(issueId)}?limit=100&offset=${offset}`
    ),
  issues: (_serviceId: string, query = "") =>
    request<ListResponse<Issue>>(
      `/errors/issues?limit=100&query=${encodeURIComponent(query)}`
    ),
  replayRecording: (_serviceId: string, replayId: string) =>
    request<ReplayRecording>(
      `/errors/replays/${encodeURIComponent(replayId)}/recording`
    ),
  replayVideoUrl: (replayId: string, segmentId: number) =>
    endpoint(
      `/errors/replays/${encodeURIComponent(replayId)}/video/${segmentId}`
    ),
  rotateUploadToken: (_serviceId: string) =>
    request<UploadToken>("/telemetry/artifact-token", { method: "POST" }),
  updateIssue: (_serviceId: string, issueId: string, status: Issue["status"]) =>
    request<Issue>(`/errors/issues/${encodeURIComponent(issueId)}`, {
      body: json({ status }),
      method: "PATCH",
    }),
};

export const downloadContent = async (
  _serviceId: string,
  contentId: string
) => {
  const response = await fetch(
    endpoint(`/errors/content/${encodeURIComponent(contentId)}`)
  );
  if (!response.ok) {
    throw new ApiError(
      `Download failed with HTTP ${response.status}`,
      response.status
    );
  }
  return response.blob();
};
