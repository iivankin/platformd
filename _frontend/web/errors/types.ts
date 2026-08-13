import type { eventWithTime as RrwebEvent } from "@sentry/rrweb";

export interface TelemetryMetadata {
  controlPlaneUrl: string;
  projectName: string;
  publicUrl?: string;
  slug: string;
}

export interface Webhook {
  createdAt: number;
  enabled: boolean;
  events: WebhookEvent[];
  id: string;
  updatedAt: number;
  url: string;
}

export type WebhookEvent =
  | "event_received"
  | "issue_created"
  | "issue_regressed"
  | "issue_resolved";

export interface App {
  id: string;
  internalDsn: string;
  name: string;
  publicDsn?: string;
  slug: string;
  webhooks: Webhook[];
}

export interface UploadToken {
  authToken: string;
}

export interface CreatedWebhook extends Webhook {
  secret: string;
}

export interface Issue {
  eventCount: number;
  firstSeen: string;
  id: string;
  lastEventId: string;
  lastSeen: string;
  level: string;
  platform: string;
  status: "ignored" | "open" | "resolved";
  title: string;
}

export interface StoredDocument {
  blob_id?: string;
  checksum?: string;
  code_id?: string;
  content_id?: string;
  debug_id?: string;
  dist?: string;
  doc_kind: string;
  environment?: string;
  event_count?: number;
  event_id?: string;
  filename?: string;
  first_seen?: string;
  ingest_id?: string;
  issue_id?: string;
  item_type?: string;
  level?: string;
  message?: string;
  object_name?: string;
  payload?: unknown;
  platform?: string;
  received_at: string;
  release?: string;
  replay_id?: string;
  sdk_name?: string;
  sdk_version?: string;
  segment_id?: number;
  sequence?: number;
  service_id: string;
  size_bytes?: number;
  status?: string;
  symbol_type?: string;
  timestamp: string;
  title?: string;
  transaction?: string;
  user?: string;
}

export interface ListResponse<T> {
  data: T[];
  total: number;
}

export interface IssueDetail {
  eventTotal: number;
  events: StoredDocument[];
  issue: Issue;
}

export interface EventDetail {
  event: StoredDocument;
  symbolication?: StoredDocument | null;
}

export interface ReplayRecording {
  errorEvents: StoredDocument[];
  events: RrwebEvent[];
  replayId: string;
  segmentCount: number;
}

export type DetailTarget =
  | { id: string; kind: "event" }
  | { id: string; kind: "issue" };

export interface SecretValue {
  label: string;
  value: string;
}
