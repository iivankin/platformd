import { Dialog } from "@base-ui/react/dialog";
import {
  Bell,
  Check,
  Cloud,
  ExternalLink,
  LoaderCircle,
  Mail,
  Play,
  Plus,
  Send,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";

import {
  createMailErrorAlert,
  createMailMetricAlert,
  deleteMailErrorAlert,
  deleteMailMetricAlert,
  fetchMailSettings,
  fetchMetricCatalog,
  fetchMetricQuery,
  saveSMTPSettings,
  sendTestMail,
  updateMailErrorAlert,
  updateMailMetricAlert,
} from "@/api";
import type {
  MailAlertService,
  MailErrorAlert,
  MailErrorAlertInput,
  MailErrorEvent,
  MailMetricAlert,
  MailMetricAlertInput,
  MailMetricOperator,
  MailSettings,
  MetricScope,
  Project,
  ServiceMetricDescriptor,
  SMTPInput,
  SMTPSettings,
} from "@/api";
import { Button } from "@/components/ui/button";
import { FormCard, SectionCard } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { PageStack } from "@/components/ui/page-stack";
import { FieldSelect } from "@/field-select";
import { metricQueryTemplates, metricSqlColumns } from "@/service-metric-model";
import { SettingsError } from "@/settings-error";

/* Helpers are declared below the page so section cards stay together. */
/* eslint-disable no-use-before-define */

const errorText = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback;

const parseRecipients = (value: string) =>
  value
    .split(/[\s,;]+/u)
    .map((item) => item.trim())
    .filter(Boolean);

const errorEvents: { label: string; value: MailErrorEvent }[] = [
  { label: "Created", value: "issue_created" },
  { label: "Regressed", value: "issue_regressed" },
  { label: "Resolved", value: "issue_resolved" },
];

const operators: { label: string; value: MailMetricOperator }[] = [
  { label: ">", value: "gt" },
  { label: "≥", value: "gte" },
  { label: "<", value: "lt" },
  { label: "≤", value: "lte" },
];

const windows: { label: string; value: number }[] = [
  { label: "1 min", value: 60 },
  { label: "5 min", value: 300 },
  { label: "15 min", value: 900 },
  { label: "1 hour", value: 3600 },
  { label: "6 hours", value: 21_600 },
  { label: "24 hours", value: 86_400 },
];

const smtpPortForEncryption = (encryption: SMTPInput["encryption"]) => {
  if (encryption === "tls") {
    return 465;
  }
  if (encryption === "none") {
    return 25;
  }
  return 587;
};

const metricCatalogScope = (
  scope: MailMetricAlertInput["scope"],
  projectId?: string,
  serviceId?: string
): MetricScope | undefined => {
  if (scope === "installation") {
    return { kind: "installation" };
  }
  if (scope === "project" && projectId) {
    return { kind: "project", projectID: projectId };
  }
  if (scope === "service" && projectId && serviceId) {
    return {
      kind: "service",
      projectID: projectId,
      serviceID: serviceId,
    };
  }
};

const alertStatusClassName = (status: "enabled" | "firing" | "paused") => {
  if (status === "firing") {
    return "text-destructive";
  }
  if (status === "enabled") {
    return "text-emerald-600";
  }
  return "text-muted-foreground";
};

const metricOperatorLabel = (operator: MailMetricOperator) =>
  operators.find((item) => item.value === operator)?.label ?? operator;

const saveLabel = (busy: boolean, editing: boolean) => {
  if (busy) {
    return "Saving…";
  }
  if (editing) {
    return "Save alert";
  }
  return "Add alert";
};

const saveSMTPLabel = (busy: boolean, passwordSet: boolean) => {
  if (busy) {
    return "Saving…";
  }
  if (passwordSet) {
    return "Replace SMTP";
  }
  return "Save SMTP";
};

const metricAlertStatus = (alert: MailMetricAlert) => {
  if (alert.firing) {
    return "firing" as const;
  }
  if (alert.enabled) {
    return "enabled" as const;
  }
  return "paused" as const;
};

const cloudflareSMTP = {
  encryption: "tls",
  host: "smtp.mx.cloudflare.net",
  port: 465,
  username: "api_token",
} as const;

const CLOUDFLARE_SMTP_DOCS =
  "https://developers.cloudflare.com/email-service/api/send-emails/smtp/";

interface CloudflareSMTPField {
  field: string;
  value: string;
}

const cloudflareSMTPFields: CloudflareSMTPField[] = [
  { field: "Host", value: cloudflareSMTP.host },
  { field: "Port", value: String(cloudflareSMTP.port) },
  { field: "Encryption", value: "TLS" },
  { field: "Username", value: cloudflareSMTP.username },
  {
    field: "Password",
    value: "Cloudflare API token with Email Sending: Edit",
  },
  {
    field: "From address",
    value: "An address on a domain onboarded for Email Sending",
  },
];

const defaultSMTP = (settings?: SMTPSettings): SMTPInput => ({
  encryption: settings?.encryption ?? "starttls",
  fromAddress: settings?.fromAddress ?? "",
  fromName: settings?.fromName ?? "",
  host: settings?.host ?? "",
  password: "",
  port: settings?.port ?? 587,
  username: settings?.username ?? "",
});

const emptyErrorDraft = (): MailErrorAlertInput => ({
  enabled: true,
  eventTypes: ["issue_created", "issue_regressed"],
  name: "",
  recipients: [],
  serviceIds: [],
});

const emptyMetricDraft = (): MailMetricAlertInput => ({
  enabled: true,
  name: "",
  operator: "gt",
  recipients: [],
  scope: "installation",
  sql: "SELECT bucket AS time, avg(value) AS value FROM metrics GROUP BY bucket",
  threshold: 0,
  windowSeconds: 300,
});

export const SettingsMailPage = ({ projects }: { projects: Project[] }) => {
  const [settings, setSettings] = useState<MailSettings>();
  const [smtp, setSMTP] = useState<SMTPInput>(defaultSMTP());
  const [testTo, setTestTo] = useState("");
  const [errorDraft, setErrorDraft] =
    useState<MailErrorAlertInput>(emptyErrorDraft);
  const [errorRecipients, setErrorRecipients] = useState("");
  const [editingErrorID, setEditingErrorID] = useState<string>();
  const [errorFormOpen, setErrorFormOpen] = useState(false);
  const [metricDraft, setMetricDraft] =
    useState<MailMetricAlertInput>(emptyMetricDraft);
  const [metricRecipients, setMetricRecipients] = useState("");
  const [editingMetricID, setEditingMetricID] = useState<string>();
  const [metricFormOpen, setMetricFormOpen] = useState(false);
  const [previewRows, setPreviewRows] = useState<number>();
  const [previewError, setPreviewError] = useState<string>();
  const [busy, setBusy] = useState("");
  const [error, setError] = useState<string>();
  const [notice, setNotice] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchMailSettings(controller.signal);
        setSettings(loaded);
        setSMTP(defaultSMTP(loaded.smtp));
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(errorText(loadError, "Unable to load mail settings"));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, []);

  const saveSMTP = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy("smtp");
    setError(undefined);
    setNotice(undefined);
    try {
      const updated = await saveSMTPSettings(smtp);
      setSettings((current) =>
        current ? { ...current, smtp: updated } : current
      );
      setSMTP({ ...smtp, password: "" });
      setNotice("SMTP saved.");
    } catch (saveError) {
      setError(errorText(saveError, "Unable to save SMTP"));
    } finally {
      setBusy("");
    }
  };

  const sendTest = async () => {
    setBusy("test");
    setError(undefined);
    setNotice(undefined);
    try {
      await sendTestMail(testTo.trim());
      setNotice(`Test email sent to ${testTo.trim()}.`);
    } catch (sendError) {
      setError(errorText(sendError, "Unable to send test email"));
    } finally {
      setBusy("");
    }
  };

  const saveErrorAlert = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy("error");
    setError(undefined);
    try {
      const input = {
        ...errorDraft,
        recipients: parseRecipients(errorRecipients),
      };
      const saved = editingErrorID
        ? await updateMailErrorAlert(editingErrorID, input)
        : await createMailErrorAlert(input);
      setSettings((current) => {
        if (!current) {
          return current;
        }
        const errorAlerts = editingErrorID
          ? current.errorAlerts.map((alert) =>
              alert.id === saved.id ? saved : alert
            )
          : [...current.errorAlerts, saved];
        return { ...current, errorAlerts };
      });
      setErrorDraft(emptyErrorDraft());
      setErrorRecipients("");
      setEditingErrorID(undefined);
      setErrorFormOpen(false);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to save error alert"));
    } finally {
      setBusy("");
    }
  };

  const saveMetricAlert = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setBusy("metric");
    setError(undefined);
    try {
      const input = {
        ...metricDraft,
        recipients: parseRecipients(metricRecipients),
      };
      const saved = editingMetricID
        ? await updateMailMetricAlert(editingMetricID, input)
        : await createMailMetricAlert(input);
      setSettings((current) => {
        if (!current) {
          return current;
        }
        const metricAlerts = editingMetricID
          ? current.metricAlerts.map((alert) =>
              alert.id === saved.id ? saved : alert
            )
          : [...current.metricAlerts, saved];
        return { ...current, metricAlerts };
      });
      setMetricDraft(emptyMetricDraft());
      setMetricRecipients("");
      setEditingMetricID(undefined);
      setMetricFormOpen(false);
      setPreviewRows(undefined);
      setPreviewError(undefined);
    } catch (saveError) {
      setError(errorText(saveError, "Unable to save metric alert"));
    } finally {
      setBusy("");
    }
  };

  const previewMetric = async () => {
    setBusy("preview");
    setPreviewError(undefined);
    if (metricDraft.scope === "project" && !metricDraft.projectId) {
      setPreviewError("Choose a project");
      setBusy("");
      return;
    }
    if (
      metricDraft.scope === "service" &&
      !(metricDraft.projectId && metricDraft.serviceId)
    ) {
      setPreviewError("Choose a service");
      setBusy("");
      return;
    }
    const scope = metricCatalogScope(
      metricDraft.scope,
      metricDraft.projectId,
      metricDraft.serviceId
    );
    if (!scope) {
      setPreviewError("Metric scope is incomplete");
      setBusy("");
      return;
    }
    try {
      const to = Date.now();
      const from = to - metricDraft.windowSeconds * 1000;
      const rows = await fetchMetricQuery(scope, {
        from,
        sql: metricDraft.sql,
        step: Math.max(
          1000,
          Math.floor((metricDraft.windowSeconds * 1000) / 60)
        ),
        to,
      });
      setPreviewRows(rows.length);
    } catch (queryError) {
      setPreviewError(errorText(queryError, "Metric query failed"));
      setPreviewRows(undefined);
    } finally {
      setBusy("");
    }
  };

  return (
    <PageStack>
      <SettingsError message={error} />
      {notice ? (
        <SectionCard className="flex items-center gap-2 border-l-2 border-l-emerald-500 px-5 py-3 text-xs">
          <Check className="size-3.5 text-emerald-600" /> {notice}
        </SectionCard>
      ) : null}

      <SMTPForm
        busy={busy}
        onChange={setSMTP}
        onSubmit={saveSMTP}
        onTest={sendTest}
        onTestTo={setTestTo}
        passwordSet={Boolean(settings?.smtp.passwordSet)}
        smtp={smtp}
        testTo={testTo}
      />

      <ErrorAlertsCard
        alerts={settings?.errorAlerts ?? []}
        busy={busy}
        draft={errorDraft}
        editingID={editingErrorID}
        formOpen={errorFormOpen}
        onChange={setErrorDraft}
        onDelete={async (alertID) => {
          setBusy(`delete-error-${alertID}`);
          setError(undefined);
          try {
            await deleteMailErrorAlert(alertID);
            setSettings((current) =>
              current
                ? {
                    ...current,
                    errorAlerts: current.errorAlerts.filter(
                      (alert) => alert.id !== alertID
                    ),
                  }
                : current
            );
            if (editingErrorID === alertID) {
              setErrorDraft(emptyErrorDraft());
              setErrorRecipients("");
              setEditingErrorID(undefined);
              setErrorFormOpen(false);
            }
          } catch (deleteError) {
            setError(errorText(deleteError, "Unable to delete error alert"));
          } finally {
            setBusy("");
          }
        }}
        onEdit={(alert) => {
          setErrorFormOpen(true);
          setEditingErrorID(alert.id);
          setErrorDraft({
            enabled: alert.enabled,
            eventTypes: alert.eventTypes,
            name: alert.name,
            recipients: alert.recipients,
            serviceIds: alert.serviceIds,
          });
          setErrorRecipients(alert.recipients.join(", "));
        }}
        onOpen={() => {
          setErrorFormOpen(true);
          setEditingErrorID(undefined);
          setErrorDraft(emptyErrorDraft());
          setErrorRecipients("");
        }}
        onRecipients={setErrorRecipients}
        onReset={() => {
          setEditingErrorID(undefined);
          setErrorDraft(emptyErrorDraft());
          setErrorRecipients("");
          setErrorFormOpen(false);
        }}
        onSubmit={saveErrorAlert}
        recipients={errorRecipients}
        services={settings?.services ?? []}
      />

      <MetricAlertsCard
        alerts={settings?.metricAlerts ?? []}
        busy={busy}
        draft={metricDraft}
        editingID={editingMetricID}
        formOpen={metricFormOpen}
        onChange={setMetricDraft}
        onDelete={async (alertID) => {
          setBusy(`delete-metric-${alertID}`);
          setError(undefined);
          try {
            await deleteMailMetricAlert(alertID);
            setSettings((current) =>
              current
                ? {
                    ...current,
                    metricAlerts: current.metricAlerts.filter(
                      (alert) => alert.id !== alertID
                    ),
                  }
                : current
            );
            if (editingMetricID === alertID) {
              setMetricDraft(emptyMetricDraft());
              setMetricRecipients("");
              setEditingMetricID(undefined);
              setMetricFormOpen(false);
            }
          } catch (deleteError) {
            setError(errorText(deleteError, "Unable to delete metric alert"));
          } finally {
            setBusy("");
          }
        }}
        onEdit={(alert) => {
          setMetricFormOpen(true);
          setEditingMetricID(alert.id);
          setMetricDraft({
            enabled: alert.enabled,
            name: alert.name,
            operator: alert.operator,
            projectId: alert.projectId,
            recipients: alert.recipients,
            scope: alert.scope,
            serviceId: alert.serviceId,
            sql: alert.sql,
            threshold: alert.threshold,
            windowSeconds: alert.windowSeconds,
          });
          setMetricRecipients(alert.recipients.join(", "));
        }}
        onOpen={() => {
          setMetricFormOpen(true);
          setEditingMetricID(undefined);
          setMetricDraft(emptyMetricDraft());
          setMetricRecipients("");
          setPreviewRows(undefined);
          setPreviewError(undefined);
        }}
        onPreview={previewMetric}
        onRecipients={setMetricRecipients}
        onReset={() => {
          setEditingMetricID(undefined);
          setMetricDraft(emptyMetricDraft());
          setMetricRecipients("");
          setMetricFormOpen(false);
          setPreviewRows(undefined);
          setPreviewError(undefined);
        }}
        onSubmit={saveMetricAlert}
        previewError={previewError}
        previewRows={previewRows}
        projects={projects}
        recipients={metricRecipients}
        services={settings?.services ?? []}
      />
    </PageStack>
  );
};

const SMTPForm = ({
  busy,
  onChange,
  onSubmit,
  onTest,
  onTestTo,
  passwordSet,
  smtp,
  testTo,
}: {
  busy: string;
  onChange: (smtp: SMTPInput) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onTest: () => void;
  onTestTo: (value: string) => void;
  passwordSet: boolean;
  smtp: SMTPInput;
  testTo: string;
}) => (
  <FormCard
    className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]"
    onSubmit={onSubmit}
  >
    <div className="px-5 py-4">
      <h2 className="flex items-center gap-2 text-xs font-medium">
        <Mail className="size-4 text-muted-foreground" /> SMTP
      </h2>
      <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
        Outgoing mail for error and metric alerts. The password is stored
        encrypted and never shown again.
      </p>
      <p className="mt-3 text-[9px] text-muted-foreground">
        {passwordSet ? "Configured" : "Not configured"}
      </p>
      <div className="mt-3">
        <CloudflareSMTPDialog
          onFill={() =>
            onChange({
              ...smtp,
              encryption: cloudflareSMTP.encryption,
              host: cloudflareSMTP.host,
              port: cloudflareSMTP.port,
              username: cloudflareSMTP.username,
            })
          }
        />
      </div>
    </div>
    <div className="grid gap-4 border-t border-border p-5 lg:border-t-0 lg:border-l">
      <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_7rem_8rem]">
        <Field
          id="smtp-host"
          label="Host"
          onChange={(host) => onChange({ ...smtp, host })}
          placeholder="smtp.example.com"
          required
          value={smtp.host}
        />
        <Field
          id="smtp-port"
          label="Port"
          onChange={(value) => onChange({ ...smtp, port: Number(value) || 0 })}
          required
          type="number"
          value={String(smtp.port || "")}
        />
        <label
          className="grid gap-1.5 text-[9px] text-muted-foreground"
          htmlFor="smtp-encryption"
        >
          Encryption
          <FieldSelect
            id="smtp-encryption"
            items={[
              { label: "STARTTLS", value: "starttls" },
              { label: "TLS", value: "tls" },
              { label: "None", value: "none" },
            ]}
            onValueChange={(encryption) => {
              if (
                encryption === "none" ||
                encryption === "starttls" ||
                encryption === "tls"
              ) {
                const port = smtpPortForEncryption(encryption);
                onChange({
                  ...smtp,
                  encryption,
                  port:
                    smtp.port === 25 || smtp.port === 465 || smtp.port === 587
                      ? port
                      : smtp.port,
                });
              }
            }}
            size="sm"
            value={smtp.encryption}
          />
        </label>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field
          id="smtp-username"
          label="Username"
          onChange={(username) => onChange({ ...smtp, username })}
          placeholder="optional"
          value={smtp.username}
        />
        <Field
          id="smtp-password"
          label="Password"
          onChange={(password) => onChange({ ...smtp, password })}
          placeholder={
            passwordSet ? "Saved — enter a new password to replace" : "Required"
          }
          required={!passwordSet}
          type="password"
          value={smtp.password}
        />
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field
          id="smtp-from"
          label="From address"
          onChange={(fromAddress) => onChange({ ...smtp, fromAddress })}
          placeholder="alerts@example.com"
          required
          type="email"
          value={smtp.fromAddress}
        />
        <Field
          id="smtp-from-name"
          label="From name"
          onChange={(fromName) => onChange({ ...smtp, fromName })}
          placeholder="platformd"
          value={smtp.fromName}
        />
      </div>
      <div className="flex flex-wrap items-end gap-2">
        <div className="min-w-48 flex-1">
          <Field
            id="smtp-test-to"
            label="Send a test to"
            onChange={onTestTo}
            placeholder="you@example.com"
            type="email"
            value={testTo}
          />
        </div>
        <Button
          disabled={busy !== "" || !passwordSet || testTo.trim() === ""}
          onClick={onTest}
          size="sm"
          type="button"
          variant="outline"
        >
          {busy === "test" ? (
            <LoaderCircle className="animate-spin" />
          ) : (
            <Send />
          )}
          Send test
        </Button>
        <Button disabled={busy !== ""} size="sm" type="submit">
          {saveSMTPLabel(busy === "smtp", passwordSet)}
        </Button>
      </div>
    </div>
  </FormCard>
);

const CloudflareSMTPDialog = ({ onFill }: { onFill: () => void }) => (
  <Dialog.Root>
    <Dialog.Trigger
      render={
        <Button size="sm" type="button" variant="outline">
          <Cloud /> Cloudflare SMTP
        </Button>
      }
    />
    <Dialog.Portal>
      <Dialog.Backdrop className="fixed inset-0 z-50 bg-black/55 backdrop-blur-[1px] data-open:animate-in data-open:fade-in data-closed:animate-out data-closed:fade-out" />
      <Dialog.Viewport className="fixed inset-0 z-50 grid place-items-center overflow-y-auto p-4">
        <Dialog.Popup className="flex max-h-[calc(100dvh-2rem)] w-full max-w-lg flex-col border border-border bg-background text-foreground shadow-2xl data-open:animate-in data-open:zoom-in-95 data-open:fade-in data-closed:animate-out data-closed:zoom-out-95 data-closed:fade-out">
          <header className="flex items-start justify-between gap-5 border-b border-border px-5 py-4">
            <div>
              <Dialog.Title className="text-sm font-medium">
                Cloudflare Email Sending
              </Dialog.Title>
              <Dialog.Description className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
                Fill SMTP with these values. Cloudflare only accepts implicit
                TLS on port 465. STARTTLS on 587 is not supported.
              </Dialog.Description>
            </div>
            <Dialog.Close
              aria-label="Close"
              className="flex size-8 shrink-0 items-center justify-center text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring"
            >
              <X className="size-4" />
            </Dialog.Close>
          </header>
          <div className="overflow-auto">
            <div className="grid grid-cols-[8rem_minmax(0,1fr)] border-b border-border bg-muted/20 px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              <span>Field</span>
              <span>Value</span>
            </div>
            {cloudflareSMTPFields.map((row) => (
              <div
                className="grid grid-cols-[8rem_minmax(0,1fr)] items-start gap-3 border-b border-border px-5 py-3 text-[10px] last:border-b-0"
                key={row.field}
              >
                <span className="text-muted-foreground">{row.field}</span>
                <code className="text-[10px] break-words text-foreground">
                  {row.value}
                </code>
              </div>
            ))}
          </div>
          <footer className="flex flex-wrap items-center gap-3 border-t border-border bg-muted/15 px-5 py-3">
            <a
              className="inline-flex items-center gap-1 text-[10px] text-foreground underline underline-offset-4"
              href={CLOUDFLARE_SMTP_DOCS}
              rel="noreferrer"
              target="_blank"
            >
              Official SMTP docs <ExternalLink className="size-3" />
            </a>
            <div className="ml-auto">
              <Dialog.Close
                render={
                  <Button onClick={onFill} size="sm" type="button">
                    Fill these fields
                  </Button>
                }
              />
            </div>
          </footer>
        </Dialog.Popup>
      </Dialog.Viewport>
    </Dialog.Portal>
  </Dialog.Root>
);

const ErrorAlertsCard = ({
  alerts,
  busy,
  draft,
  editingID,
  formOpen,
  onChange,
  onDelete,
  onEdit,
  onOpen,
  onRecipients,
  onReset,
  onSubmit,
  recipients,
  services,
}: {
  alerts: MailErrorAlert[];
  busy: string;
  draft: MailErrorAlertInput;
  editingID?: string;
  formOpen: boolean;
  onChange: (draft: MailErrorAlertInput) => void;
  onDelete: (alertID: string) => void;
  onEdit: (alert: MailErrorAlert) => void;
  onOpen: () => void;
  onRecipients: (value: string) => void;
  onReset: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  recipients: string;
  services: MailAlertService[];
}) => (
  <SectionCard>
    <div className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
      <div className="px-5 py-4">
        <h2 className="flex items-center gap-2 text-xs font-medium">
          <Bell className="size-4 text-muted-foreground" /> Error alerts
        </h2>
        <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
          Email when a Sentry issue is created, regressed, or resolved. Leave
          services empty to cover every service.
        </p>
      </div>
      <div className="border-t border-border lg:border-t-0 lg:border-l">
        {alerts.length ? (
          <div className="divide-y divide-border border-b border-border">
            {alerts.map((alert) => (
              <AlertRow
                busy={busy}
                key={alert.id}
                onDelete={() => onDelete(alert.id)}
                onEdit={() => onEdit(alert)}
                status={alert.enabled ? "enabled" : "paused"}
                subtitle={`${alert.eventTypes.length} events · ${
                  alert.serviceIds.length === 0
                    ? "all services"
                    : `${alert.serviceIds.length} services`
                } · ${alert.recipients.join(", ")}`}
                title={alert.name}
              />
            ))}
          </div>
        ) : (
          <p className="border-b border-border px-5 py-4 text-[10px] text-muted-foreground">
            No error alerts yet.
          </p>
        )}
        {formOpen ? (
          <form className="grid gap-4 p-5" onSubmit={onSubmit}>
            <div className="grid gap-3 sm:grid-cols-2">
              <Field
                id="error-alert-name"
                label="Name"
                onChange={(name) => onChange({ ...draft, name })}
                placeholder="Production errors"
                required
                value={draft.name}
              />
              <Field
                id="error-alert-recipients"
                label="Recipients"
                onChange={onRecipients}
                placeholder="ops@example.com, oncall@example.com"
                required
                value={recipients}
              />
            </div>
            <fieldset>
              <legend className="mb-1.5 text-[9px] text-muted-foreground">
                Events
              </legend>
              <div className="flex flex-wrap gap-2">
                {errorEvents.map((event) => {
                  const active = draft.eventTypes.includes(event.value);
                  return (
                    <button
                      className={`h-7 border px-2 text-[10px] ${
                        active
                          ? "border-foreground bg-foreground text-background"
                          : "border-border text-muted-foreground hover:text-foreground"
                      }`}
                      key={event.value}
                      onClick={() =>
                        onChange({
                          ...draft,
                          eventTypes: active
                            ? draft.eventTypes.filter(
                                (value) => value !== event.value
                              )
                            : [...draft.eventTypes, event.value],
                        })
                      }
                      type="button"
                    >
                      {event.label}
                    </button>
                  );
                })}
              </div>
            </fieldset>
            <ServicePicker
              onChange={(serviceIds) => onChange({ ...draft, serviceIds })}
              selected={draft.serviceIds}
              services={services}
            />
            <label
              className="flex items-center gap-2 text-[10px]"
              htmlFor="error-alert-enabled"
            >
              <Checkbox
                checked={draft.enabled}
                id="error-alert-enabled"
                onCheckedChange={(checked) =>
                  onChange({ ...draft, enabled: checked === true })
                }
              />
              Enabled
            </label>
            <div className="flex gap-2">
              <Button
                disabled={busy !== "" || draft.eventTypes.length === 0}
                size="sm"
                type="submit"
              >
                {saveLabel(busy === "error", Boolean(editingID))}
              </Button>
              <Button onClick={onReset} size="sm" type="button" variant="ghost">
                Cancel
              </Button>
            </div>
          </form>
        ) : (
          <div className="px-5 py-3">
            <Button onClick={onOpen} size="sm" type="button" variant="outline">
              <Plus /> Add alert
            </Button>
          </div>
        )}
      </div>
    </div>
  </SectionCard>
);

const MetricAlertsCard = ({
  alerts,
  busy,
  draft,
  editingID,
  formOpen,
  onChange,
  onDelete,
  onEdit,
  onOpen,
  onPreview,
  onRecipients,
  onReset,
  onSubmit,
  previewError,
  previewRows,
  projects,
  recipients,
  services,
}: {
  alerts: MailMetricAlert[];
  busy: string;
  draft: MailMetricAlertInput;
  editingID?: string;
  formOpen: boolean;
  onChange: (draft: MailMetricAlertInput) => void;
  onDelete: (alertID: string) => void;
  onEdit: (alert: MailMetricAlert) => void;
  onOpen: () => void;
  onPreview: () => void;
  onRecipients: (value: string) => void;
  onReset: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  previewError?: string;
  previewRows?: number;
  projects: Project[];
  recipients: string;
  services: MailAlertService[];
}) => {
  const [catalog, setCatalog] = useState<ServiceMetricDescriptor[]>([]);
  const scopedServices = useMemo(
    () =>
      draft.projectId
        ? services.filter((service) => service.projectId === draft.projectId)
        : services,
    [draft.projectId, services]
  );
  const catalogProjectId = draft.projectId;
  const catalogScopeKind = draft.scope;
  const catalogServiceId = draft.serviceId;
  const catalogScope = metricCatalogScope(
    catalogScopeKind,
    catalogProjectId,
    catalogServiceId
  );
  useEffect(() => {
    if (!formOpen) {
      return;
    }
    const scope = metricCatalogScope(
      catalogScopeKind,
      catalogProjectId,
      catalogServiceId
    );
    if (!scope) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        setCatalog(await fetchMetricCatalog(scope, controller.signal));
      } catch (loadError) {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setCatalog([]);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [catalogProjectId, catalogScopeKind, catalogServiceId, formOpen]);
  return (
    <SectionCard>
      <div className="grid lg:grid-cols-[14rem_minmax(18rem,1fr)]">
        <div className="px-5 py-4">
          <h2 className="flex items-center gap-2 text-xs font-medium">
            <Plus className="size-4 text-muted-foreground" /> Metric alerts
          </h2>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Custom ClickHouse SQL over <code>metrics</code>. Mail fires when the
            latest value crosses the threshold, and again when it recovers.
          </p>
        </div>
        <div className="border-t border-border lg:border-t-0 lg:border-l">
          {alerts.length ? (
            <div className="divide-y divide-border border-b border-border">
              {alerts.map((alert) => (
                <AlertRow
                  busy={busy}
                  key={alert.id}
                  onDelete={() => onDelete(alert.id)}
                  onEdit={() => onEdit(alert)}
                  status={metricAlertStatus(alert)}
                  subtitle={`${alert.scope} · value ${metricOperatorLabel(alert.operator)} ${alert.threshold} · ${alert.recipients.join(", ")}`}
                  title={alert.name}
                />
              ))}
            </div>
          ) : (
            <p className="border-b border-border px-5 py-4 text-[10px] text-muted-foreground">
              No metric alerts yet.
            </p>
          )}
          {formOpen ? (
            <form onSubmit={onSubmit}>
              <div className="grid gap-4 p-5">
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field
                    id="metric-alert-name"
                    label="Name"
                    onChange={(name) => onChange({ ...draft, name })}
                    placeholder="Checkout latency"
                    required
                    value={draft.name}
                  />
                  <Field
                    id="metric-alert-recipients"
                    label="Recipients"
                    onChange={onRecipients}
                    placeholder="ops@example.com"
                    required
                    value={recipients}
                  />
                </div>
                <div className="grid gap-3 sm:grid-cols-3">
                  <label
                    className="grid gap-1.5 text-[9px] text-muted-foreground"
                    htmlFor="metric-alert-scope"
                  >
                    Scope
                    <FieldSelect
                      id="metric-alert-scope"
                      items={[
                        { label: "Installation", value: "installation" },
                        { label: "Project", value: "project" },
                        { label: "Service", value: "service" },
                      ]}
                      onValueChange={(scope) => {
                        if (
                          scope === "installation" ||
                          scope === "project" ||
                          scope === "service"
                        ) {
                          onChange({
                            ...draft,
                            projectId:
                              scope === "installation"
                                ? undefined
                                : draft.projectId,
                            scope,
                            serviceId:
                              scope === "service" ? draft.serviceId : undefined,
                          });
                        }
                      }}
                      size="sm"
                      value={draft.scope}
                    />
                  </label>
                  {draft.scope === "installation" ? null : (
                    <label
                      className="grid gap-1.5 text-[9px] text-muted-foreground"
                      htmlFor="metric-alert-project"
                    >
                      Project
                      <FieldSelect
                        id="metric-alert-project"
                        items={[
                          { label: "Choose project", value: "__none__" },
                          ...projects.map((project) => ({
                            label: project.name,
                            value: project.id,
                          })),
                        ]}
                        onValueChange={(projectId) =>
                          onChange({
                            ...draft,
                            projectId:
                              projectId === "__none__" ? undefined : projectId,
                            serviceId:
                              draft.serviceId &&
                              services.some(
                                (service) =>
                                  service.id === draft.serviceId &&
                                  service.projectId === projectId
                              )
                                ? draft.serviceId
                                : undefined,
                          })
                        }
                        size="sm"
                        value={draft.projectId ?? "__none__"}
                      />
                    </label>
                  )}
                  {draft.scope === "service" ? (
                    <label
                      className="grid gap-1.5 text-[9px] text-muted-foreground"
                      htmlFor="metric-alert-service"
                    >
                      Service
                      <FieldSelect
                        id="metric-alert-service"
                        items={[
                          { label: "Choose service", value: "__none__" },
                          ...scopedServices.map((service) => ({
                            label: `${service.projectName}/${service.name}`,
                            value: service.id,
                          })),
                        ]}
                        onValueChange={(serviceId) =>
                          onChange({
                            ...draft,
                            serviceId:
                              serviceId === "__none__" ? undefined : serviceId,
                          })
                        }
                        size="sm"
                        value={draft.serviceId ?? "__none__"}
                      />
                    </label>
                  ) : null}
                </div>
                <MetricAlertSql
                  catalog={catalogScope ? catalog : []}
                  draft={draft}
                  onChange={onChange}
                />
                <div className="grid gap-3 sm:grid-cols-3">
                  <label
                    className="grid gap-1.5 text-[9px] text-muted-foreground"
                    htmlFor="metric-alert-operator"
                  >
                    Operator
                    <FieldSelect
                      id="metric-alert-operator"
                      items={operators}
                      onValueChange={(operator) => {
                        if (
                          operator === "gt" ||
                          operator === "gte" ||
                          operator === "lt" ||
                          operator === "lte"
                        ) {
                          onChange({ ...draft, operator });
                        }
                      }}
                      size="sm"
                      value={draft.operator}
                    />
                  </label>
                  <Field
                    id="metric-threshold"
                    label="Threshold"
                    onChange={(value) =>
                      onChange({ ...draft, threshold: Number(value) })
                    }
                    required
                    type="number"
                    value={String(draft.threshold)}
                  />
                  <label
                    className="grid gap-1.5 text-[9px] text-muted-foreground"
                    htmlFor="metric-alert-window"
                  >
                    Window
                    <FieldSelect
                      id="metric-alert-window"
                      items={windows.map((window) => ({
                        label: window.label,
                        value: String(window.value),
                      }))}
                      onValueChange={(value) =>
                        onChange({ ...draft, windowSeconds: Number(value) })
                      }
                      size="sm"
                      value={String(draft.windowSeconds)}
                    />
                  </label>
                </div>
                <label
                  className="flex items-center gap-2 text-[10px]"
                  htmlFor="metric-alert-enabled"
                >
                  <Checkbox
                    checked={draft.enabled}
                    id="metric-alert-enabled"
                    onCheckedChange={(checked) =>
                      onChange({ ...draft, enabled: checked === true })
                    }
                  />
                  Enabled
                </label>
                {previewError ? (
                  <p className="text-[9px] text-destructive">{previewError}</p>
                ) : null}
                {previewRows === undefined ? null : (
                  <p className="text-[9px] text-muted-foreground">
                    Preview returned {previewRows} point
                    {previewRows === 1 ? "" : "s"}.
                  </p>
                )}
                <div className="flex flex-wrap gap-2">
                  <Button
                    disabled={busy !== "" || !draft.sql.trim()}
                    onClick={onPreview}
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    {busy === "preview" ? (
                      <LoaderCircle className="animate-spin" />
                    ) : (
                      <Play />
                    )}
                    Run query
                  </Button>
                  <Button disabled={busy !== ""} size="sm" type="submit">
                    {saveLabel(busy === "metric", Boolean(editingID))}
                  </Button>
                  <Button
                    onClick={onReset}
                    size="sm"
                    type="button"
                    variant="ghost"
                  >
                    Cancel
                  </Button>
                </div>
              </div>
            </form>
          ) : (
            <div className="px-5 py-3">
              <Button
                onClick={onOpen}
                size="sm"
                type="button"
                variant="outline"
              >
                <Plus /> Add alert
              </Button>
            </div>
          )}
        </div>
      </div>
    </SectionCard>
  );
};

const MetricAlertSql = ({
  catalog,
  draft,
  onChange,
}: {
  catalog: ServiceMetricDescriptor[];
  draft: MailMetricAlertInput;
  onChange: (draft: MailMetricAlertInput) => void;
}) => {
  const templates = metricQueryTemplates(catalog[0]);
  return (
    <div className="grid border border-border lg:grid-cols-[minmax(0,1fr)_16rem]">
      <div className="min-w-0 p-3">
        <div className="flex flex-wrap items-end gap-2">
          <div className="mr-auto">
            <p className="text-[9px] text-muted-foreground">ClickHouse SQL</p>
            <p className="text-[9px] leading-4 text-muted-foreground">
              Read from <code>metrics</code>. Return <code>time</code>,{" "}
              <code>value</code>, and optional <code>series</code>.
            </p>
          </div>
          {templates.map((template) => (
            <Button
              key={template.label}
              onClick={() => onChange({ ...draft, sql: template.sql })}
              size="sm"
              type="button"
              variant="ghost"
            >
              {template.label}
            </Button>
          ))}
        </div>
        <textarea
          autoCapitalize="off"
          autoCorrect="off"
          className="mt-3 min-h-56 w-full resize-y border border-border bg-background px-3 py-3 font-mono text-[10px] leading-5 outline-none focus:border-foreground"
          id="metric-alert-sql"
          maxLength={16_384}
          onChange={(event) => onChange({ ...draft, sql: event.target.value })}
          required
          spellCheck={false}
          value={draft.sql}
        />
      </div>
      <aside className="min-w-0 border-t border-border px-3 py-3 lg:border-t-0 lg:border-l">
        <h3 className="text-[10px] font-medium">metrics schema</h3>
        <p className="mt-1 text-[9px] leading-4 text-muted-foreground">
          Restricted to the selected alert scope. Cumulative deltas and
          histogram buckets are prepared by chDB.
        </p>
        <div className="mt-3 max-h-56 overflow-auto border-y border-border">
          {metricSqlColumns.map(([name, type, description]) => (
            <div
              className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 border-b border-border/70 px-2 py-2 last:border-b-0"
              key={name}
              title={description}
            >
              <code className="truncate text-[9px]">{name}</code>
              <span className="text-[8px] text-muted-foreground">{type}</span>
            </div>
          ))}
        </div>
        <h3 className="mt-4 text-[10px] font-medium">Observed metrics</h3>
        <div className="mt-2 max-h-36 overflow-auto border-y border-border">
          {catalog.length ? (
            catalog.map((metric) => (
              <button
                className="flex w-full items-center justify-between gap-3 border-b border-border/70 px-2 py-2 text-left last:border-b-0 hover:bg-muted"
                key={metric.name}
                onClick={() => {
                  const [template] = metricQueryTemplates(metric);
                  if (template) {
                    onChange({ ...draft, sql: template.sql });
                  }
                }}
                type="button"
              >
                <code className="truncate text-[9px]">{metric.name}</code>
                <span className="shrink-0 text-[8px] text-muted-foreground">
                  {metric.kind}
                </span>
              </button>
            ))
          ) : (
            <p className="px-2 py-3 text-[9px] text-muted-foreground">
              No application metrics yet.
            </p>
          )}
        </div>
      </aside>
    </div>
  );
};

const AlertRow = ({
  busy,
  onDelete,
  onEdit,
  status,
  subtitle,
  title,
}: {
  busy: string;
  onDelete: () => void;
  onEdit: () => void;
  status: "enabled" | "firing" | "paused";
  subtitle: string;
  title: string;
}) => (
  <div className="grid gap-2 px-5 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center">
    <div className="min-w-0">
      <p className="flex items-center gap-2 text-[11px] font-medium">
        {title}
        <span
          className={`text-[8px] tracking-[0.08em] uppercase ${alertStatusClassName(status)}`}
        >
          {status}
        </span>
      </p>
      <p className="mt-0.5 truncate text-[9px] text-muted-foreground">
        {subtitle}
      </p>
    </div>
    <div className="flex gap-1">
      <Button onClick={onEdit} size="sm" type="button" variant="ghost">
        Edit
      </Button>
      <Button
        disabled={busy.startsWith("delete-")}
        onClick={onDelete}
        size="icon"
        type="button"
        variant="ghost"
      >
        <Trash2 />
      </Button>
    </div>
  </div>
);

const ServicePicker = ({
  onChange,
  selected,
  services,
}: {
  onChange: (serviceIds: string[]) => void;
  selected: string[];
  services: MailAlertService[];
}) => {
  const selectedSet = new Set(selected);
  return (
    <fieldset>
      <legend className="mb-1.5 text-[9px] text-muted-foreground">
        Services
      </legend>
      {services.length === 0 ? (
        <p className="text-[10px] text-muted-foreground">No services yet.</p>
      ) : (
        <div className="grid max-h-40 gap-1 overflow-auto border border-border p-2">
          {services.map((service) => {
            const active = selectedSet.has(service.id);
            return (
              <label
                className="flex items-center gap-2 px-1 py-1 text-[10px]"
                key={service.id}
              >
                <Checkbox
                  checked={active}
                  onCheckedChange={(checked) =>
                    onChange(
                      checked === true
                        ? [...selected, service.id]
                        : selected.filter((id) => id !== service.id)
                    )
                  }
                />
                <span className="truncate">
                  {service.projectName}/{service.name}
                </span>
              </label>
            );
          })}
        </div>
      )}
      <p className="mt-1 text-[9px] text-muted-foreground">
        {selected.length === 0 ? "All services" : `${selected.length} selected`}
      </p>
    </fieldset>
  );
};

const Field = ({
  id,
  label,
  onChange,
  placeholder,
  required,
  type = "text",
  value,
}: {
  id: string;
  label: string;
  onChange: (value: string) => void;
  placeholder?: string;
  required?: boolean;
  type?: string;
  value: string;
}) => (
  <label className="grid gap-1.5 text-[9px] text-muted-foreground" htmlFor={id}>
    {label}
    <Input
      id={id}
      onChange={(event) => onChange(event.target.value)}
      placeholder={placeholder}
      required={required}
      type={type}
      value={value}
    />
  </label>
);
