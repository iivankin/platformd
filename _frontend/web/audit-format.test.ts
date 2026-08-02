import { describe, expect, test } from "bun:test";

import type { AuditEvent } from "@/api";
import {
  formatAuditAction,
  formatAuditActor,
  formatAuditMetadata,
  formatAuditTarget,
} from "@/audit-format";

const event: AuditEvent = {
  action: "service.update",
  actorId: "developer@example.com",
  actorKind: "access",
  createdAt: 1,
  id: "event",
  metadata: {
    actorEmail: "developer@example.com",
    durationMillis: 1250,
    enabled: true,
    name: "api",
    serviceId: "service-identifier-that-is-long",
  },
  result: "succeeded",
  targetId: "service-identifier-that-is-long",
  targetKind: "service",
};

describe("audit display formatting", () => {
  test("uses human labels with a readable fallback for new actions", () => {
    expect(formatAuditAction("service.update")).toBe(
      "Service settings updated"
    );
    expect(formatAuditAction("custom_resource.rotate_key")).toBe(
      "Custom resource rotate key"
    );
  });

  test("presents actors and targets without exposing technical kinds", () => {
    expect(formatAuditActor(event)).toEqual({
      primary: "developer@example.com",
      secondary: "Cloudflare Access",
    });
    expect(formatAuditTarget(event)).toEqual({
      primary: "Service · api",
      secondary: "service-…s-long",
    });
  });

  test("uses the webhook URL as its readable target", () => {
    expect(
      formatAuditTarget({
        ...event,
        metadata: { url: "https://hooks.example.com/deploy" },
        targetId: "webhook-identifier",
        targetKind: "project_webhook",
      })
    ).toEqual({
      primary: "Project webhook · https://hooks.example.com/deploy",
      secondary: "webhook-identifier",
    });
  });

  test("turns metadata into concise labels and hides duplicated actor email", () => {
    expect(formatAuditMetadata(event.metadata)).toEqual([
      { key: "durationMillis", label: "Duration", value: "1.3 s" },
      { key: "enabled", label: "Enabled", value: "Yes" },
      { key: "serviceId", label: "Service", value: "service-…s-long" },
    ]);
  });
});
