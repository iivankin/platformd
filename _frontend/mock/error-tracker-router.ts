import type {
  ApiToken,
  App,
  CreatedApiToken,
  CreatedApp,
  CreatedWebhook,
  Issue,
  StoredDocument,
  WebhookEvent,
} from "../web/error-tracker/types";
import { newID } from "../web/id";
import type { ErrorTrackerMockState } from "./error-tracker-state";

interface RouteContext {
  app?: App;
  child?: string;
  childId?: string;
  grandchild?: string;
  request: Request;
  resourceId?: string;
  state: ErrorTrackerMockState;
  url: URL;
}

const jsonError = (message: string, status: number) =>
  Response.json({ message }, { status });

const createId = (prefix: string) =>
  `${prefix}_${crypto.randomUUID().replaceAll("-", "")}`;

const createUploadToken = () =>
  `et_${crypto.randomUUID().replaceAll("-", "")}${crypto.randomUUID().replaceAll("-", "")}`;

const body = async (request: Request) => {
  const value: unknown = await request.json();
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : {};
};

const appDsn = (state: ErrorTrackerMockState, app: App) => {
  const dsn = new URL(state.tracker.publicUrl);
  dsn.username = app.publicKey;
  dsn.pathname = `/${app.projectId}`;
  return dsn.toString().replace(/\/$/u, "");
};

const matchesQuery = (value: unknown, query: string) =>
  JSON.stringify(value).toLowerCase().includes(query.toLowerCase());

const list = <T>(data: T[]) => Response.json({ data, total: data.length });

const queryResults = <T>(items: T[], query: string) =>
  query ? items.filter((item) => matchesQuery(item, query)) : items;

const nextProjectId = (apps: App[]) => {
  let maximum = 0n;
  for (const app of apps) {
    const current = BigInt(app.projectId);
    if (current > maximum) {
      maximum = current;
    }
  }
  return (maximum + 1n).toString();
};

const handleTracker = ({ request, state }: RouteContext) => {
  if (request.method === "GET") {
    return Response.json(state.tracker);
  }
  return jsonError("method not allowed", 405);
};

const handleTokens = async ({ request, resourceId, state }: RouteContext) => {
  if (request.method === "GET" && !resourceId) {
    return Response.json(state.apiTokens);
  }
  if (request.method === "DELETE" && resourceId) {
    state.apiTokens = state.apiTokens.filter(
      (token) => token.id !== resourceId
    );
    return new Response(null, { status: 204 });
  }
  if (request.method !== "POST" || resourceId) {
    return jsonError("method not allowed", 405);
  }
  const input = await body(request);
  if (typeof input.name !== "string") {
    return jsonError("name is required", 400);
  }
  const token: CreatedApiToken = {
    appId: typeof input.appId === "string" ? input.appId : null,
    createdAt: new Date().toISOString(),
    id: createId("token"),
    name: input.name,
    role: input.role === "admin" ? "admin" : "read",
    token: createId("et"),
  };
  const stored: ApiToken = { ...token };
  state.apiTokens.push(stored);
  return Response.json(token, { status: 201 });
};

const handleAppsCollection = async ({ request, state }: RouteContext) => {
  if (request.method === "GET") {
    return Response.json(state.apps);
  }
  if (request.method !== "POST") {
    return jsonError("method not allowed", 405);
  }
  const input = await body(request);
  if (typeof input.name !== "string" || typeof input.slug !== "string") {
    return jsonError("name and slug are required", 400);
  }
  const timestamp = new Date().toISOString();
  const app: CreatedApp = {
    authToken: createUploadToken(),
    createdAt: timestamp,
    dsn: "",
    id: newID(),
    name: input.name,
    projectId: nextProjectId(state.apps),
    publicKey: crypto.randomUUID().replaceAll("-", ""),
    slug: input.slug,
    updatedAt: timestamp,
    webhooks: [],
  };
  app.dsn = appDsn(state, app);
  state.apps.push(app);
  return Response.json(app, { status: 201 });
};

const appEvents = (state: ErrorTrackerMockState, app: App) =>
  state.events.filter((event) => event.app_id === app.id);

const appIssues = (state: ErrorTrackerMockState, app: App) => {
  const issueIds = new Set(
    appEvents(state, app).flatMap((event) =>
      event.issue_id ? [event.issue_id] : []
    )
  );
  return state.issues.filter((issue) => issueIds.has(issue.id));
};

const handleIssues = async ({
  app,
  childId,
  request,
  state,
  url,
}: RouteContext) => {
  if (!app) {
    return jsonError("application not found", 404);
  }
  const issues = appIssues(state, app);
  if (!childId && request.method === "GET") {
    return list(
      queryResults(issues, url.searchParams.get("query")?.trim() ?? "")
    );
  }
  const issue = issues.find((candidate) => candidate.id === childId);
  if (!issue) {
    return jsonError("issue not found", 404);
  }
  if (request.method === "GET") {
    const events = appEvents(state, app).filter(
      (event) => event.issue_id === issue.id
    );
    return Response.json({ eventTotal: events.length, events, issue });
  }
  if (request.method !== "PATCH") {
    return jsonError("method not allowed", 405);
  }
  const input = await body(request);
  if (!["open", "resolved", "ignored"].includes(String(input.status))) {
    return jsonError("invalid status", 400);
  }
  issue.status = String(input.status) as Issue["status"];
  return Response.json(issue);
};

const symbolication = (app: App, event: StoredDocument): StoredDocument => ({
  app_id: app.id,
  doc_kind: "symbolication",
  event_id: event.event_id,
  payload: {
    errors: [],
    stacktraces: [
      {
        frames: [
          {
            colno: 11,
            filename: "webpack:///src/checkout/cart.ts",
            function: "reserveInventory",
            lineno: 71,
          },
          {
            colno: 17,
            filename: "webpack:///src/checkout/submit.ts",
            function: "finalizeOrder",
            lineno: 184,
          },
        ],
      },
    ],
    status: "completed",
  },
  project_id: app.projectId,
  received_at: event.received_at,
  timestamp: event.timestamp,
});

const handleEvents = ({ app, childId, request, state, url }: RouteContext) => {
  if (!app) {
    return jsonError("application not found", 404);
  }
  const events = appEvents(state, app);
  if (!childId && request.method === "GET") {
    return list(
      queryResults(events, url.searchParams.get("query")?.trim() ?? "")
    );
  }
  const event = events.find((candidate) => candidate.event_id === childId);
  return event
    ? Response.json({ event, symbolication: symbolication(app, event) })
    : jsonError("event not found", 404);
};

const handleReplays = ({
  app,
  childId,
  grandchild,
  request,
  state,
}: RouteContext) => {
  if (!app) {
    return jsonError("application not found", 404);
  }
  const replayItems = state.replayItems.filter(
    (item) => item.app_id === app.id
  );
  if (!childId && request.method === "GET") {
    return list(replayItems.filter((item) => item.doc_kind === "replay_event"));
  }
  if (grandchild === "recording" && request.method === "GET") {
    return state.replayRecording.replayId === childId
      ? Response.json(state.replayRecording)
      : jsonError("replay recording not found", 404);
  }
  const items = replayItems.filter((item) => item.replay_id === childId);
  return items.length > 0
    ? Response.json({ items, total: items.length })
    : jsonError("replay not found", 404);
};

const handleArtifacts = ({ app, request, state, url }: RouteContext) => {
  if (!app || request.method !== "GET") {
    return jsonError("not found", 404);
  }
  const artifacts = state.artifacts.filter((item) => item.app_id === app.id);
  return list(
    queryResults(artifacts, url.searchParams.get("query")?.trim() ?? "")
  );
};

const handleWebhooks = async ({ app, childId, request }: RouteContext) => {
  if (!app) {
    return jsonError("application not found", 404);
  }
  if (childId && request.method === "DELETE") {
    app.webhooks = app.webhooks.filter((webhook) => webhook.id !== childId);
    return new Response(null, { status: 204 });
  }
  if (childId || request.method !== "POST") {
    return jsonError("method not allowed", 405);
  }
  const input = await body(request);
  if (typeof input.url !== "string" || !Array.isArray(input.events)) {
    return jsonError("url and events are required", 400);
  }
  const timestamp = new Date().toISOString();
  const webhook: CreatedWebhook = {
    createdAt: timestamp,
    enabled: true,
    events: input.events as WebhookEvent[],
    id: createId("webhook"),
    secret: createId("secret"),
    updatedAt: timestamp,
    url: input.url,
  };
  app.webhooks.push(webhook);
  return Response.json(webhook, { status: 201 });
};

const handleUploadToken = ({ app, request }: RouteContext) => {
  if (!app) {
    return jsonError("application not found", 404);
  }
  if (request.method !== "POST") {
    return jsonError("method not allowed", 405);
  }
  app.updatedAt = new Date().toISOString();
  return Response.json({ authToken: createUploadToken() });
};

const handleApp = (context: RouteContext) => {
  const { app, child, childId, request } = context;
  if (!app) {
    return jsonError("application not found", 404);
  }
  if (!child && request.method === "GET") {
    return Response.json(app);
  }
  switch (child) {
    case "artifacts": {
      return handleArtifacts(context);
    }
    case "content": {
      return childId && request.method === "GET"
        ? new Response(`mock content for ${childId}`, {
            headers: { "content-type": "application/octet-stream" },
          })
        : jsonError("not found", 404);
    }
    case "events": {
      return handleEvents(context);
    }
    case "issues": {
      return handleIssues(context);
    }
    case "replays": {
      return handleReplays(context);
    }
    case "upload-token": {
      return handleUploadToken(context);
    }
    case "webhooks": {
      return handleWebhooks(context);
    }
    default: {
      return jsonError("not found", 404);
    }
  }
};

export const handleErrorTrackerMock = (
  request: Request,
  state: ErrorTrackerMockState
) => {
  const url = new URL(request.url);
  const parts = url.pathname.split("/").filter(Boolean).map(decodeURIComponent);
  const [resource, resourceId, child, childId, grandchild] = parts.slice(2);
  const context: RouteContext = {
    app: state.apps.find((candidate) => candidate.id === resourceId),
    child,
    childId,
    grandchild,
    request,
    resourceId,
    state,
    url,
  };
  switch (resource) {
    case "apps": {
      return resourceId ? handleApp(context) : handleAppsCollection(context);
    }
    case "tokens": {
      return handleTokens(context);
    }
    case "tracker": {
      return handleTracker(context);
    }
    default: {
      return jsonError("not found", 404);
    }
  }
};
