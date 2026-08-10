import { describe, expect, test } from "bun:test";

import type {
  App,
  EventDetail,
  Issue,
  IssueDetail,
  ReplayRecording,
} from "../web/error-tracker/types";
import { handleErrorTrackerMock } from "./error-tracker-router";
import { createErrorTrackerMockState } from "./error-tracker-state";

const request = (
  path: string,
  init: RequestInit = {},
  state = createErrorTrackerMockState()
) =>
  handleErrorTrackerMock(
    new Request(`http://127.0.0.1:3101${path}`, {
      ...init,
      headers: init.body ? { "content-type": "application/json" } : undefined,
    }),
    state
  );

describe("error tracker mock API", () => {
  test("serves an issue and its symbolicated latest event", async () => {
    const state = createErrorTrackerMockState();
    const issueId = state.issues[0]?.id;
    expect(issueId).toBeDefined();

    const issueResponse = await request(
      `/api/v1/apps/k9n2p4r7t5v8x3z6b1d4f7h2/issues/${issueId}`,
      {},
      state
    );
    const detail = (await issueResponse.json()) as IssueDetail;
    expect(detail.issue.title).toContain("CheckoutInvariantError");
    expect(detail.events).toHaveLength(2);

    const eventResponse = await request(
      `/api/v1/apps/k9n2p4r7t5v8x3z6b1d4f7h2/events/${detail.issue.lastEventId}`,
      {},
      state
    );
    const eventDetail = (await eventResponse.json()) as {
      symbolication: { payload: { status: string } };
    };
    expect(eventDetail.symbolication.payload.status).toBe("completed");
  });

  test("persists UI mutations until the mock process restarts", async () => {
    const state = createErrorTrackerMockState();
    const [issue] = state.issues;
    expect(issue).toBeDefined();

    const updateResponse = await request(
      `/api/v1/apps/k9n2p4r7t5v8x3z6b1d4f7h2/issues/${issue?.id}`,
      { body: JSON.stringify({ status: "resolved" }), method: "PATCH" },
      state
    );
    const updated = (await updateResponse.json()) as Issue;
    expect(updated.status).toBe("resolved");

    const trackerResponse = await request(
      "/api/v1/tracker",
      {
        body: JSON.stringify({ publicUrl: "https://errors.example.test" }),
        method: "PATCH",
      },
      state
    );
    expect(trackerResponse.status).toBe(405);

    const appsResponse = await request("/api/v1/apps", {}, state);
    const apps = (await appsResponse.json()) as App[];
    expect(apps[0]?.dsn).toStartWith(
      "http://be1ca1a7343244188fae4c8a4c53d55e@127.0.0.1:3101/"
    );
  });

  test("rotates the single application upload token", async () => {
    const response = await request(
      "/api/v1/apps/k9n2p4r7t5v8x3z6b1d4f7h2/upload-token",
      {
        method: "POST",
      }
    );
    const payload = (await response.json()) as { authToken: string };

    expect(response.ok).toBe(true);
    expect(payload.authToken).toMatch(/^et_[a-f0-9]{64}$/u);
  });

  test("creates Sentry-compatible numeric project IDs", async () => {
    const state = createErrorTrackerMockState();
    const response = await request(
      "/api/v1/apps",
      {
        body: JSON.stringify({ name: "Worker", slug: "worker" }),
        method: "POST",
      },
      state
    );
    const app = (await response.json()) as App;

    expect(response.status).toBe(201);
    expect(app.projectId).toBe("3");
    expect(new URL(app.dsn).pathname).toBe("/3");
  });

  test("serves structured event context and its playable replay", async () => {
    const state = createErrorTrackerMockState();
    const linkedEvent = state.events.find((event) => event.replay_id);
    expect(linkedEvent?.event_id).toBeDefined();

    const eventResponse = await request(
      `/api/v1/apps/k9n2p4r7t5v8x3z6b1d4f7h2/events/${linkedEvent?.event_id}`,
      {},
      state
    );
    const detail = (await eventResponse.json()) as EventDetail;
    expect(detail.event.payload).toMatchObject({
      breadcrumbs: { values: expect.any(Array) },
      contexts: expect.any(Object),
      request: expect.any(Object),
      user: expect.any(Object),
    });

    const replayResponse = await request(
      `/api/v1/apps/k9n2p4r7t5v8x3z6b1d4f7h2/replays/${linkedEvent?.replay_id}/recording`,
      {},
      state
    );
    const recording = (await replayResponse.json()) as ReplayRecording;
    expect(recording.errorEvents).toHaveLength(1);
    expect(recording.errorEvents[0]?.payload).toBeUndefined();
    expect(recording.events.some((event) => event.type === 2)).toBe(true);
    expect(recording.events.some((event) => event.type === 4)).toBe(true);
  });
});
