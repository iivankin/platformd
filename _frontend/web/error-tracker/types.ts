import type { eventWithTime as RrwebEvent } from "@sentry/rrweb";

export type ViewName =
  | "issues"
  | "events"
  | "replays"
  | "artifacts"
  | "settings";

export interface Tracker {
  adminAuthRequired: boolean;
  name: string;
  publicUrl: string;
  slug: string;
}

export interface Webhook {
  createdAt: string;
  enabled: boolean;
  events: WebhookEvent[];
  id: string;
  updatedAt: string;
  url: string;
}

export type WebhookEvent =
  | "event_received"
  | "issue_created"
  | "issue_regressed"
  | "issue_resolved";

export interface App {
  createdAt: string;
  dsn: string;
  id: string;
  name: string;
  projectId: string;
  publicKey: string;
  slug: string;
  updatedAt: string;
  webhooks: Webhook[];
}

export interface CreatedApp extends App {
  authToken: string;
}

export interface UploadToken {
  authToken: string;
}

export interface ApiToken {
  appId?: string | null;
  createdAt: string;
  id: string;
  name: string;
  role: "admin" | "read";
}

export interface CreatedApiToken extends ApiToken {
  token: string;
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
  app_id: string;
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
  project_id: string;
  received_at: string;
  release?: string;
  replay_id?: string;
  sdk_name?: string;
  sdk_version?: string;
  segment_id?: number;
  sequence?: number;
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

export interface ReplayDetail {
  items: StoredDocument[];
  total: number;
}

export interface ReplayRecording {
  errorEvents: StoredDocument[];
  events: RrwebEvent[];
  replayId: string;
  segmentCount: number;
}

export type DetailTarget =
  | { id: string; kind: "event" }
  | { id: string; kind: "issue" }
  | { id: string; kind: "replay" };

export interface SecretValue {
  label: string;
  value: string;
}
