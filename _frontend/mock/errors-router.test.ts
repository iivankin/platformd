import { describe, expect, test } from "bun:test";

import type {
  EventDetail,
  Issue,
  IssueDetail,
  ReplayRecording,
} from "../web/errors/types";
import { handleErrorsMock } from "./errors-router";
import { createErrorsMockState } from "./errors-state";

const request = (
  path: string,
  init: RequestInit = {},
  state = createErrorsMockState()
) =>
  handleErrorsMock(
    new Request(`http://127.0.0.1:3101${path}`, {
      ...init,
      headers: init.body ? { "content-type": "application/json" } : undefined,
    }),
    state
  );

describe("service errors mock API", () => {
  test("serves an issue and its symbolicated latest event", async () => {
    const state = createErrorsMockState();
    const issueId = state.issues[0]?.id;
    expect(issueId).toBeDefined();

    const issueResponse = await request(`/issues/${issueId}`, {}, state);
    const detail = (await issueResponse.json()) as IssueDetail;
    expect(detail.issue.title).toContain("CheckoutInvariantError");
    expect(detail.events).toHaveLength(2);

    const eventResponse = await request(
      `/events/${detail.issue.lastEventId}`,
      {},
      state
    );
    const eventDetail = (await eventResponse.json()) as {
      symbolication: { payload: { status: string } };
    };
    expect(eventDetail.symbolication.payload.status).toBe("completed");
  });

  test("persists issue mutations until the mock process restarts", async () => {
    const state = createErrorsMockState();
    const [issue] = state.issues;
    const updateResponse = await request(
      `/issues/${issue?.id}`,
      { body: JSON.stringify({ status: "resolved" }), method: "PATCH" },
      state
    );
    const updated = (await updateResponse.json()) as Issue;
    expect(updated.status).toBe("resolved");
  });

  test("serves structured event context and its playable replay", async () => {
    const state = createErrorsMockState();
    const linkedEvent = state.events.find((event) => event.replay_id);
    const eventResponse = await request(
      `/events/${linkedEvent?.event_id}`,
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
      `/replays/${linkedEvent?.replay_id}/recording`,
      {},
      state
    );
    const recording = (await replayResponse.json()) as ReplayRecording;
    expect(recording.errorEvents).toHaveLength(1);
    expect(recording.events.some((event) => event.type === 2)).toBe(true);
    expect(recording.events.some((event) => event.type === 4)).toBe(true);
  });
});
