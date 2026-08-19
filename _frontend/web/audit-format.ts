import type { AuditEvent } from "@/api";

export const auditActionOptions = [
  ["api_token.create", "API token created"],
  ["api_token.revoke", "API token revoked"],
  ["backup.control_target.set", "Control backup destination changed"],
  ["backup.policy.set", "Backup policy updated"],
  ["backup_target.delete", "Backup destination deleted"],
  ["backup_target.set", "Backup destination saved"],
  ["cloudflare_dns.configure", "Cloudflare DNS configured"],
  ["cloudflare_mesh.configure", "Cloudflare private network configured"],
  ["container_terminal.end", "Container terminal closed"],
  ["container_terminal.start", "Container terminal opened"],
  ["control.restore", "Control plane restored"],
  ["disk_pressure.transition", "Disk pressure changed"],
  ["host.delete", "Child server removed"],
  ["host.join", "Child server joined"],
  ["host_join_token.create", "Join token created"],
  ["host_join_token.delete", "Join token revoked"],
  ["installation.access_configuration.set", "Cloudflare Access changed"],
  ["installation.admin_hostname.set", "Admin hostname changed"],
  ["installation.console_passphrase_reset", "Console passphrase reset"],
  ["installation.create", "Platform initialized"],
  ["installation.origin_certificate.add", "Origin certificate added"],
  ["installation.origin_certificate.delete", "Origin certificate deleted"],
  ["installation.origin_certificate.replace", "Origin certificate replaced"],
  ["mail_error_alert.create", "Error mail alert created"],
  ["mail_error_alert.delete", "Error mail alert deleted"],
  ["mail_error_alert.update", "Error mail alert updated"],
  ["mail_metric_alert.create", "Metric mail alert created"],
  ["mail_metric_alert.delete", "Metric mail alert deleted"],
  ["mail_metric_alert.update", "Metric mail alert updated"],
  ["network_gateway.create", "Network gateway created"],
  ["network_gateway.delete", "Network gateway deleted"],
  ["network_gateway.update", "Network gateway updated"],
  ["object_store.create", "Object store created"],
  ["object_store.restore", "Object store restored"],
  ["port_forward.ticket.create", "Port forwarding authorized"],
  ["postgres.create", "PostgreSQL database created"],
  ["postgres.extension.install", "PostgreSQL extension installed"],
  ["postgres.extension.uninstall", "PostgreSQL extension removed"],
  ["postgres.query", "PostgreSQL query executed"],
  ["postgres.restore", "PostgreSQL database restored"],
  ["postgres.version_change", "PostgreSQL version changed"],
  ["project.create", "Project created"],
  ["project.delete", "Project deleted"],
  ["project_webhook.create", "Project webhook created"],
  ["project_webhook.delete", "Project webhook deleted"],
  ["project_webhook.update", "Project webhook updated"],
  ["recovery.complete", "Recovery completed"],
  ["redis.create", "Redis database created"],
  ["redis.data.mutate", "Redis data changed"],
  ["redis.restore", "Redis database restored"],
  ["redis.version_change", "Redis version changed"],
  ["server.exec", "Server command executed"],
  ["server_terminal.end", "Server terminal closed"],
  ["server_terminal.start", "Server terminal opened"],
  ["service.create", "Service created"],
  ["service.delete", "Service deleted"],
  ["service.deploy_version", "Service version deployed"],
  ["service.deployment_remove", "Service deployment removed"],
  ["service.domain.attach", "Service domain attached"],
  ["service.domain.detach", "Service domain detached"],
  ["service.domain.move", "Service domain moved"],
  ["service.domain.update", "Service domain updated"],
  ["service.listener.attach", "Public port attached"],
  ["service.listener.detach", "Public port detached"],
  ["service.listener.update", "Public port updated"],
  ["service.redeploy", "Service redeployed"],
  ["service.update", "Service settings updated"],
  ["smtp.configure", "SMTP configured"],
  ["volume.create", "Volume created"],
  ["volume.delete", "Volume deleted"],
] as const;

const auditActionLabels = new Map<string, string>(auditActionOptions);

export const auditActionItems = Object.fromEntries([
  ["__all-actions__", "All actions"],
  ...auditActionOptions,
]);

const targetLabels: Record<string, string> = {
  api_token: "API token",
  backup_target: "Backup destination",
  cloudflare_dns: "Cloudflare DNS",
  cloudflare_mesh: "Cloudflare private network",
  host: "Child server",
  host_join_token: "Join token",
  installation: "Platform",
  mail_error_alert: "Error mail alert",
  mail_metric_alert: "Metric mail alert",
  network_gateway: "Network gateway",
  object_store: "Object store",
  port_forward_ticket: "Port-forward session",
  postgres: "PostgreSQL database",
  project: "Project",
  project_webhook: "Project webhook",
  redis: "Redis database",
  server: "Server",
  service: "Service",
  smtp: "SMTP",
  volume: "Volume",
};

const metadataLabels: Record<string, string> = {
  appliedSnapshotPolicy: "Snapshot policy applied",
  audience: "Application AUD",
  bucket: "Bucket",
  cancelled: "Cancelled",
  certificateId: "Certificate",
  closeReason: "Close reason",
  command: "Command",
  cron: "Schedule",
  deleteBackups: "Backups deleted",
  durationMillis: "Duration",
  enabled: "Enabled",
  encryption: "Encryption",
  endpoint: "Endpoint",
  errorClass: "Error",
  eventTypes: "Events",
  executionError: "Execution error",
  exitCode: "Exit code",
  expiresAt: "Expires",
  extension: "Extension",
  finishedAt: "Finished",
  fromAddress: "From address",
  host: "Host",
  hostname: "Hostname",
  imageDigest: "Image digest",
  imageTag: "Image version",
  listenPort: "Listen port",
  manifestCount: "Manifests",
  mode: "Mode",
  name: "Name",
  objectCount: "Objects",
  operator: "Operator",
  port: "Port",
  prefix: "Path prefix",
  previousAudience: "Previous application AUD",
  previousImageDigest: "Previous image digest",
  previousImageTag: "Previous image version",
  previousTeamDomain: "Previous team domain",
  previousVolumeId: "Previous volume",
  projectId: "Project",
  projectName: "Project name",
  protocol: "Protocol",
  publicPort: "Public port",
  region: "Region",
  resourceId: "Resource",
  resourceKind: "Resource type",
  retentionCount: "Copies retained",
  role: "Role",
  rowCount: "Rows",
  serviceId: "Service",
  services: "Services",
  sourceIp: "Source IP",
  startedAt: "Started",
  stderrTruncated: "Error output truncated",
  stdoutTruncated: "Output truncated",
  tagCount: "Tags",
  targetId: "Backup destination",
  targetPort: "Target port",
  teamDomain: "Team domain",
  threshold: "Threshold",
  ticketId: "Authorization",
  timedOut: "Timed out",
  transport: "Transport",
  url: "URL",
  volumeCount: "Volumes",
  volumeId: "Volume",
  windowSeconds: "Window",
};

const shortID = (value: string) =>
  value.length > 18 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value;

const humanizeIdentifier = (value: string) => {
  const words = value
    .replaceAll(/(?<=[a-z\d])(?=[A-Z])/gu, " ")
    .replaceAll(/[._-]+/gu, " ")
    .trim();
  return words ? `${words[0]?.toUpperCase()}${words.slice(1)}` : value;
};

export const formatAuditAction = (action: string) =>
  auditActionLabels.get(action) ?? humanizeIdentifier(action);

export const formatAuditStatus = (result: AuditEvent["result"]) =>
  result === "succeeded" ? "Succeeded" : "Failed";

export const formatAuditActor = (event: AuditEvent) => {
  switch (event.actorKind) {
    case "access": {
      return {
        primary:
          typeof event.metadata.actorEmail === "string"
            ? event.metadata.actorEmail
            : event.actorId,
        secondary: "Cloudflare Access",
      };
    }
    case "token": {
      return { primary: "API token", secondary: shortID(event.actorId) };
    }
    case "system": {
      return {
        primary:
          event.actorId === "platformd"
            ? "Platformd"
            : humanizeIdentifier(event.actorId),
        secondary: "System",
      };
    }
    case "local_root": {
      return {
        primary: "Local administrator",
        secondary:
          event.actorId === "init"
            ? "Local console"
            : humanizeIdentifier(event.actorId),
      };
    }
    default: {
      return { primary: event.actorId, secondary: "Unknown actor" };
    }
  }
};

export const formatAuditTarget = (event: AuditEvent) => {
  let targetName: string | undefined;
  if (typeof event.metadata.name === "string") {
    targetName = event.metadata.name;
  } else if (typeof event.metadata.projectName === "string") {
    targetName = event.metadata.projectName;
  } else if (
    event.targetKind === "project_webhook" &&
    typeof event.metadata.url === "string"
  ) {
    targetName = event.metadata.url;
  }
  const targetKind =
    targetLabels[event.targetKind] ?? humanizeIdentifier(event.targetKind);
  return {
    primary: targetName ? `${targetKind} · ${targetName}` : targetKind,
    secondary: shortID(event.targetId),
  };
};

export interface AuditMetadataItem {
  key: string;
  label: string;
  value: string;
}

const formatDuration = (milliseconds: number) => {
  if (milliseconds < 1000) {
    return `${milliseconds} ms`;
  }
  if (milliseconds < 60_000) {
    return `${Number((milliseconds / 1000).toFixed(1))} s`;
  }
  return `${Number((milliseconds / 60_000).toFixed(1))} min`;
};

type MetadataValueFormatter = (value: unknown) => string;

const formatBooleanValue: MetadataValueFormatter = (value) =>
  value === true || value === "true" ? "Yes" : "No";

const formatTimestampValue: MetadataValueFormatter = (value) =>
  typeof value === "number" ? new Date(value).toLocaleString() : String(value);

const formatDurationValue: MetadataValueFormatter = (value) => {
  const milliseconds = Number(String(value));
  return Number.isFinite(milliseconds)
    ? formatDuration(milliseconds)
    : String(value);
};

const formatRoleValue: MetadataValueFormatter = (value) => {
  if (value === "admin") {
    return "Administrator";
  }
  return value === "read" ? "Read only" : humanizeIdentifier(String(value));
};

const formatResourceKindValue: MetadataValueFormatter = (value) => {
  const kind = String(value);
  return targetLabels[kind] ?? humanizeIdentifier(kind);
};

const formatEnumValue: MetadataValueFormatter = (value) =>
  humanizeIdentifier(String(value));

const formatUppercaseValue: MetadataValueFormatter = (value) =>
  String(value).toUpperCase();

const formatEventTypesValue: MetadataValueFormatter = (value) =>
  Array.isArray(value)
    ? value.map((item) => humanizeIdentifier(String(item))).join(", ")
    : String(value);

const metadataValueFormatters: Record<string, MetadataValueFormatter> = {
  cancelled: formatBooleanValue,
  closeReason: formatEnumValue,
  deleteBackups: formatBooleanValue,
  durationMillis: formatDurationValue,
  enabled: formatBooleanValue,
  errorClass: formatEnumValue,
  eventTypes: formatEventTypesValue,
  expiresAt: formatTimestampValue,
  finishedAt: formatTimestampValue,
  mode: formatEnumValue,
  protocol: formatUppercaseValue,
  resourceKind: formatResourceKindValue,
  role: formatRoleValue,
  startedAt: formatTimestampValue,
  stderrTruncated: formatBooleanValue,
  stdoutTruncated: formatBooleanValue,
  timedOut: formatBooleanValue,
  transport: formatUppercaseValue,
};

const formatMetadataValue = (key: string, value: unknown): string => {
  const formatter = metadataValueFormatters[key];
  if (formatter) {
    return formatter(value);
  }
  if (typeof value === "boolean") {
    return formatBooleanValue(value);
  }
  if (Array.isArray(value)) {
    return value.map(String).join(", ");
  }
  if (value && typeof value === "object") {
    return Object.entries(value)
      .map(
        ([nestedKey, nestedValue]) =>
          `${humanizeIdentifier(nestedKey)}: ${String(nestedValue)}`
      )
      .join(", ");
  }
  if (typeof value === "string" && key.endsWith("Id")) {
    return shortID(value);
  }
  return String(value);
};

export const formatAuditMetadata = (
  metadata: AuditEvent["metadata"]
): AuditMetadataItem[] =>
  Object.entries(metadata).flatMap(([key, value]) => {
    if (
      key === "actorEmail" ||
      key === "name" ||
      key === "projectName" ||
      value === "" ||
      value === undefined
    ) {
      return [];
    }
    return [
      {
        key,
        label: metadataLabels[key] ?? humanizeIdentifier(key),
        value: formatMetadataValue(key, value),
      },
    ];
  });
