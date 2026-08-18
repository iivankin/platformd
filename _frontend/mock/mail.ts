import type {
  MailAlertService,
  MailErrorAlert,
  MailErrorEvent,
  MailMetricAlert,
  MailMetricOperator,
  MailSettings,
  SMTPSettings,
} from "../web/api";
import {
  booleanField,
  json,
  mockError,
  noContent,
  numberField,
  readObject,
  stringField,
} from "./http";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

const errorEvents = new Set<MailErrorEvent>([
  "issue_created",
  "issue_regressed",
  "issue_resolved",
]);

const operators = new Set<MailMetricOperator>(["gt", "gte", "lt", "lte"]);

const stringArray = (value: unknown) =>
  Array.isArray(value)
    ? value.filter((item): item is string => typeof item === "string")
    : [];

const mailServices = (state: MockState): MailAlertService[] =>
  Object.values(state.services)
    .map((service) => {
      const project = state.projects.find(
        (item) => item.id === service.projectId
      );
      return {
        id: service.id,
        name: service.name,
        projectId: service.projectId,
        projectName: project?.name ?? service.projectId,
      };
    })
    .toSorted(
      (left, right) =>
        left.projectName.localeCompare(right.projectName) ||
        left.name.localeCompare(right.name) ||
        left.id.localeCompare(right.id)
    );

const mailResponse = (state: MockState): MailSettings => ({
  ...state.mail,
  services: mailServices(state),
});

const parseRecipients = (input: Record<string, unknown>) => {
  const recipients = stringArray(input.recipients).map((item) => item.trim());
  if (recipients.length < 1 || recipients.length > 20) {
    return mockError(
      "mail_settings_failed",
      "Alerts require between 1 and 20 recipients."
    );
  }
  return recipients;
};

const parseErrorAlert = (
  input: Record<string, unknown>,
  state: MockState
): MailErrorAlert | Response => {
  const name = stringField(input, "name").trim();
  const recipients = parseRecipients(input);
  if (recipients instanceof Response) {
    return recipients;
  }
  const eventTypes = stringArray(input.eventTypes).filter(
    (item): item is MailErrorEvent => errorEvents.has(item as MailErrorEvent)
  );
  if (name.length < 1 || name.length > 80 || eventTypes.length === 0) {
    return mockError(
      "mail_settings_failed",
      "Error alert input is incomplete."
    );
  }
  const serviceIds = stringArray(input.serviceIds);
  if (serviceIds.length > 50) {
    return mockError(
      "mail_settings_failed",
      "Alerts require at most 50 services."
    );
  }
  for (const serviceId of serviceIds) {
    if (!state.services[serviceId]) {
      return mockError(
        "mail_alert_service_missing",
        "Mail alert references a missing service"
      );
    }
  }
  return {
    createdAt: mockNow(),
    enabled: booleanField(input, "enabled", true),
    eventTypes,
    id: "",
    name,
    recipients,
    serviceIds,
    updatedAt: mockNow(),
  };
};

const parseMetricAlert = (
  input: Record<string, unknown>
): MailMetricAlert | Response => {
  const name = stringField(input, "name").trim();
  const recipients = parseRecipients(input);
  if (recipients instanceof Response) {
    return recipients;
  }
  const scope = stringField(input, "scope");
  const operator = stringField(input, "operator") as MailMetricOperator;
  const sql = stringField(input, "sql").trim();
  const windowSeconds = numberField(input, "windowSeconds", 0);
  const projectId = stringField(input, "projectId");
  const serviceId = stringField(input, "serviceId");
  const validScope =
    (scope === "installation" && !projectId && !serviceId) ||
    (scope === "project" && projectId && !serviceId) ||
    (scope === "service" && projectId && serviceId);
  if (
    name.length < 1 ||
    name.length > 80 ||
    !validScope ||
    !operators.has(operator) ||
    sql.length < 1 ||
    sql.length > 16_384 ||
    windowSeconds < 60 ||
    windowSeconds > 86_400
  ) {
    return mockError(
      "mail_settings_failed",
      "Metric alert input is incomplete."
    );
  }
  return {
    createdAt: mockNow(),
    enabled: booleanField(input, "enabled", true),
    firing: false,
    id: "",
    name,
    operator,
    ...(projectId ? { projectId } : {}),
    recipients,
    scope: scope as MailMetricAlert["scope"],
    ...(serviceId ? { serviceId } : {}),
    sql,
    threshold: numberField(input, "threshold", 0),
    updatedAt: mockNow(),
    windowSeconds,
  };
};

const putSMTP = async (request: Request, state: MockState) => {
  const input = await readObject(request);
  const host = stringField(input, "host").trim().toLowerCase();
  const port = numberField(input, "port", 0);
  const fromAddress = stringField(input, "fromAddress").trim();
  const encryption = stringField(input, "encryption");
  const password = stringField(input, "password");
  if (
    !host ||
    port < 1 ||
    port > 65_535 ||
    !fromAddress ||
    !["none", "starttls", "tls"].includes(encryption)
  ) {
    return mockError(
      "mail_settings_failed",
      "SMTP settings input is incomplete"
    );
  }
  if (!password && !state.mail.smtp.passwordSet) {
    return mockError("mail_settings_failed", "SMTP password is required");
  }
  const smtp: SMTPSettings = {
    configured: true,
    encryption: encryption as SMTPSettings["encryption"],
    fromAddress,
    fromName: stringField(input, "fromName").trim(),
    host,
    passwordSet: true,
    port,
    updatedAt: mockNow(),
    username: stringField(input, "username").trim(),
  };
  state.mail = { ...state.mail, smtp };
  return json(smtp);
};

const handleErrorAlerts = async (
  request: Request,
  state: MockState,
  alertID?: string
) => {
  if (request.method === "POST" && !alertID) {
    if (state.mail.errorAlerts.length >= 20) {
      return mockError("mail_alert_limit", "Mail alert limit reached", 409);
    }
    const parsed = parseErrorAlert(await readObject(request), state);
    if (parsed instanceof Response) {
      return parsed;
    }
    const alert = { ...parsed, id: nextMockID(state, "mail-error") };
    state.mail = {
      ...state.mail,
      errorAlerts: [...state.mail.errorAlerts, alert],
    };
    return json(alert, 201);
  }
  if (!alertID) {
    return;
  }
  const existing = state.mail.errorAlerts.find((alert) => alert.id === alertID);
  if (!existing) {
    return mockError("mail_alert_not_found", "Mail alert not found", 404);
  }
  if (request.method === "DELETE") {
    state.mail = {
      ...state.mail,
      errorAlerts: state.mail.errorAlerts.filter((item) => item.id !== alertID),
    };
    return noContent();
  }
  if (request.method !== "PUT") {
    return;
  }
  const parsed = parseErrorAlert(await readObject(request), state);
  if (parsed instanceof Response) {
    return parsed;
  }
  const alert = { ...parsed, createdAt: existing.createdAt, id: existing.id };
  state.mail = {
    ...state.mail,
    errorAlerts: state.mail.errorAlerts.map((item) =>
      item.id === alertID ? alert : item
    ),
  };
  return json(alert);
};

const handleMetricAlerts = async (
  request: Request,
  state: MockState,
  alertID?: string
) => {
  if (request.method === "POST" && !alertID) {
    if (state.mail.metricAlerts.length >= 20) {
      return mockError("mail_alert_limit", "Mail alert limit reached", 409);
    }
    const parsed = parseMetricAlert(await readObject(request));
    if (parsed instanceof Response) {
      return parsed;
    }
    const alert = { ...parsed, id: nextMockID(state, "mail-metric") };
    state.mail = {
      ...state.mail,
      metricAlerts: [...state.mail.metricAlerts, alert],
    };
    return json(alert, 201);
  }
  if (!alertID) {
    return;
  }
  const existing = state.mail.metricAlerts.find(
    (alert) => alert.id === alertID
  );
  if (!existing) {
    return mockError("mail_alert_not_found", "Mail alert not found", 404);
  }
  if (request.method === "DELETE") {
    state.mail = {
      ...state.mail,
      metricAlerts: state.mail.metricAlerts.filter(
        (item) => item.id !== alertID
      ),
    };
    return noContent();
  }
  if (request.method !== "PUT") {
    return;
  }
  const parsed = parseMetricAlert(await readObject(request));
  if (parsed instanceof Response) {
    return parsed;
  }
  const alert = { ...parsed, createdAt: existing.createdAt, id: existing.id };
  state.mail = {
    ...state.mail,
    metricAlerts: state.mail.metricAlerts.map((item) =>
      item.id === alertID ? alert : item
    ),
  };
  return json(alert);
};

const postTestMail = async (request: Request, state: MockState) => {
  const input = await readObject(request);
  if (!state.mail.smtp.configured) {
    return mockError("smtp_not_configured", "SMTP is not configured");
  }
  if (!stringField(input, "to").includes("@")) {
    return mockError("mail_settings_failed", "email address is invalid");
  }
  return json({ sent: true });
};

export const handleMailAPI = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  if (segments[0] !== "settings" || segments[1] !== "mail") {
    return undefined;
  }
  const [resource, alertID, extra] = segments.slice(2);
  if (extra !== undefined) {
    return undefined;
  }
  if (!resource && request.method === "GET") {
    return json(mailResponse(state));
  }
  if (resource === "smtp" && !alertID && request.method === "PUT") {
    return await putSMTP(request, state);
  }
  if (resource === "test" && !alertID && request.method === "POST") {
    return await postTestMail(request, state);
  }
  if (resource === "error-alerts") {
    return await handleErrorAlerts(request, state, alertID);
  }
  if (resource === "metric-alerts") {
    return await handleMetricAlerts(request, state, alertID);
  }
  return undefined;
};
