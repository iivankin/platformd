import type { Issue, StoredDocument } from "../web/errors/types";
import type { ErrorsMockState } from "./errors-state";

interface RouteContext {
  child?: string;
  request: Request;
  resourceId?: string;
  state: ErrorsMockState;
  url: URL;
}

const jsonError = (message: string, status: number) =>
  Response.json({ message }, { status });

const body = async (request: Request) => {
  const value: unknown = await request.json();
  return value && typeof value === "object"
    ? (value as Record<string, unknown>)
    : {};
};

const matchesQuery = (value: unknown, query: string) =>
  JSON.stringify(value).toLowerCase().includes(query.toLowerCase());

const list = <T>(data: T[]) => Response.json({ data, total: data.length });

const queryResults = <T>(items: T[], query: string) =>
  query ? items.filter((item) => matchesQuery(item, query)) : items;

const handleIssues = async ({
  request,
  resourceId,
  state,
  url,
}: RouteContext) => {
  if (!resourceId && request.method === "GET") {
    return list(
      queryResults(state.issues, url.searchParams.get("query")?.trim() ?? "")
    );
  }
  const issue = state.issues.find((candidate) => candidate.id === resourceId);
  if (!issue) {
    return jsonError("issue not found", 404);
  }
  if (request.method === "GET") {
    const events = state.events
      .filter((event) => event.issue_id === issue.id)
      .toSorted(
        (left, right) =>
          Date.parse(right.timestamp) - Date.parse(left.timestamp)
      );
    const offset = Math.max(0, Number(url.searchParams.get("offset") ?? 0));
    const limit = Math.min(
      100,
      Math.max(1, Number(url.searchParams.get("limit") ?? 100))
    );
    const distributions = [
      ["release", "storefront@2.8.1"],
      ["environment", "production"],
      ["browser", "Chrome 127"],
      ["device", "Desktop"],
    ].map(([key, value]) => ({ count: events.length, key, value }));
    const [latest] = events;
    const first = events.at(-1);
    const recommended =
      events.find((event) => event.replay_id && event.user) ?? latest;
    return Response.json({
      activity: events.length > 0 ? [{ bin: 31, count: events.length }] : [],
      distributions,
      eventTotal: events.length,
      events: events.slice(offset, offset + limit),
      firstEventId: first?.event_id ?? "",
      issue,
      latestEventId: latest?.event_id ?? "",
      recommendedEventId: recommended?.event_id ?? "",
      userCount: new Set(events.map((event) => event.user).filter(Boolean))
        .size,
    });
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

const symbolication = (event: StoredDocument): StoredDocument => ({
  doc_kind: "symbolication",
  event_id: event.event_id,
  payload: {
    errors: [],
    stacktraces: [
      {
        frames: [
          {
            colno: 11,
            context_line:
              "throw new CheckoutInvariantError('Inventory reservation expired');",
            filename: "webpack:///src/checkout/cart.ts",
            function: "reserveInventory",
            in_app: true,
            lineno: 71,
            module: "checkout/cart",
            post_context: ["}", "", "export async function checkout() {"],
            pre_context: [
              "if (!reservation.active) {",
              "  inventory.release(reservation.id);",
            ],
          },
          {
            colno: 17,
            filename: "webpack:///src/checkout/submit.ts",
            function: "finalizeOrder",
            in_app: true,
            lineno: 184,
            module: "checkout/submit",
          },
          {
            colno: 21,
            filename: "https://shop.example.com/chunk-runtime.js",
            function: "dispatch",
            in_app: false,
            lineno: 1,
            module: "runtime",
          },
        ],
      },
    ],
    status: "completed",
  },
  received_at: event.received_at,
  service_id: event.service_id,
  timestamp: event.timestamp,
});

const handleEvents = ({ request, resourceId, state, url }: RouteContext) => {
  if (!resourceId && request.method === "GET") {
    return list(
      queryResults(state.events, url.searchParams.get("query")?.trim() ?? "")
    );
  }
  const event = state.events.find(
    (candidate) => candidate.event_id === resourceId
  );
  return event
    ? Response.json({ event, symbolication: symbolication(event) })
    : jsonError("event not found", 404);
};

const handleReplays = ({ child, request, resourceId, state }: RouteContext) => {
  if (!resourceId && request.method === "GET") {
    return list(
      state.replayItems.filter((item) => item.doc_kind === "replay_event")
    );
  }
  if (child === "recording" && request.method === "GET") {
    return state.replayRecording.replayId === resourceId
      ? Response.json(state.replayRecording)
      : jsonError("replay recording not found", 404);
  }
  const items = state.replayItems.filter(
    (item) => item.replay_id === resourceId
  );
  return items.length > 0
    ? Response.json({ items, total: items.length })
    : jsonError("replay not found", 404);
};

const handleArtifacts = ({ request, state, url }: RouteContext) =>
  request.method === "GET"
    ? list(
        queryResults(
          state.artifacts,
          url.searchParams.get("query")?.trim() ?? ""
        )
      )
    : jsonError("method not allowed", 405);

export const handleErrorsMock = (request: Request, state: ErrorsMockState) => {
  const url = new URL(request.url);
  const [resource, resourceId, child] = url.pathname
    .split("/")
    .filter(Boolean)
    .map(decodeURIComponent);
  const context = { child, request, resourceId, state, url };
  switch (resource) {
    case "artifacts": {
      return handleArtifacts(context);
    }
    case "content": {
      return resourceId && request.method === "GET"
        ? new Response(`mock content for ${resourceId}`, {
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
    default: {
      return jsonError("not found", 404);
    }
  }
};
