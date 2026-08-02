import {
  AlertTriangle,
  Check,
  ChevronDown,
  LoaderCircle,
  Pencil,
  Send,
  Sparkles,
  Trash2,
  Webhook,
} from "lucide-react";
import { useEffect, useState } from "react";

import {
  createProjectWebhook,
  deleteProjectWebhook,
  fetchProjectWebhooks,
  testProjectWebhook,
  updateProjectWebhook,
} from "@/api";
import type { ProjectWebhook, ProjectWebhookEventType } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";

const webhookEvents: {
  className: string;
  label: string;
  value: ProjectWebhookEventType;
}[] = [
  {
    className: "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300",
    label: "Started",
    value: "deployment.started",
  },
  {
    className:
      "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
    label: "Succeeded",
    value: "deployment.succeeded",
  },
  {
    className: "border-destructive/30 bg-destructive/10 text-destructive",
    label: "Failed",
    value: "deployment.failed",
  },
  {
    className:
      "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300",
    label: "Interrupted",
    value: "deployment.interrupted",
  },
  {
    className: "border-border bg-muted/40 text-muted-foreground",
    label: "Skipped",
    value: "deployment.skipped",
  },
];

const criticalEvents = new Set<ProjectWebhookEventType>([
  "deployment.failed",
  "deployment.interrupted",
]);

const validWebhookURL = (value: string) => {
  try {
    const url = new URL(value);
    return (
      (url.protocol === "http:" || url.protocol === "https:") &&
      Boolean(url.hostname) &&
      !url.username &&
      !url.password &&
      !url.hash
    );
  } catch {
    return false;
  }
};

const WebhookEventPicker = ({
  eventTypes,
  onChange,
}: {
  eventTypes: ProjectWebhookEventType[];
  onChange: (eventTypes: ProjectWebhookEventType[]) => void;
}) => {
  const [open, setOpen] = useState(false);
  const selected = new Set(eventTypes);

  const toggle = (eventType: ProjectWebhookEventType) => {
    onChange(
      selected.has(eventType)
        ? eventTypes.filter((value) => value !== eventType)
        : [...eventTypes, eventType]
    );
  };

  return (
    <div>
      <button
        aria-expanded={open}
        className="flex h-9 w-full items-center border border-input bg-transparent px-3 text-left text-xs transition-colors outline-none hover:bg-muted/30 focus-visible:border-ring focus-visible:ring-1 focus-visible:ring-ring"
        onClick={() => setOpen((value) => !value)}
        type="button"
      >
        <span
          className={cn(
            "min-w-0 flex-1 truncate",
            eventTypes.length === 0 && "text-muted-foreground"
          )}
        >
          {eventTypes.length === 0
            ? "Choose events…"
            : `${eventTypes.length} ${eventTypes.length === 1 ? "event" : "events"} selected`}
        </span>
        <ChevronDown
          className={cn("size-3.5 text-muted-foreground", open && "rotate-180")}
        />
      </button>

      {open ? (
        <div className="border-x border-b border-border bg-background px-4 py-4">
          <p className="text-[10px] font-medium">Deployment</p>
          <div className="mt-3 grid grid-cols-2 gap-2 sm:grid-cols-3">
            {webhookEvents.map((event) => {
              const active = selected.has(event.value);
              return (
                <button
                  aria-pressed={active}
                  className={cn(
                    "flex h-9 items-center gap-2 border px-3 text-left text-[10px] transition-opacity outline-none focus-visible:ring-1 focus-visible:ring-ring",
                    event.className,
                    !active && "opacity-45 hover:opacity-75"
                  )}
                  key={event.value}
                  onClick={() => toggle(event.value)}
                  type="button"
                >
                  {active ? (
                    <Check className="size-3.5" />
                  ) : (
                    <span className="size-3.5 border border-current/50" />
                  )}
                  {event.label}
                </button>
              );
            })}
          </div>
        </div>
      ) : null}

      <Button
        className="mt-2"
        onClick={() =>
          onChange(
            webhookEvents
              .map((event) => event.value)
              .filter(
                (eventType) =>
                  selected.has(eventType) || criticalEvents.has(eventType)
              )
          )
        }
        size="sm"
        variant="outline"
      >
        <Sparkles /> Add critical events
      </Button>
    </div>
  );
};

export const ProjectWebhooksSettings = ({
  projectID,
}: {
  projectID: string;
}) => {
  const [webhooks, setWebhooks] = useState<ProjectWebhook[]>([]);
  const [url, setURL] = useState("");
  const [eventTypes, setEventTypes] = useState<ProjectWebhookEventType[]>([]);
  const [editingID, setEditingID] = useState<string>();
  const [deleteCandidate, setDeleteCandidate] = useState<string>();
  const [busy, setBusy] = useState<"delete" | "save" | "test">();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [notice, setNotice] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const load = async () => {
      try {
        const loaded = await fetchProjectWebhooks(projectID, controller.signal);
        setWebhooks(loaded.webhooks);
        setError(undefined);
      } catch (loadError) {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        setError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load project webhooks"
        );
      } finally {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [projectID]);

  const resetForm = () => {
    setURL("");
    setEventTypes([]);
    setEditingID(undefined);
    setNotice(undefined);
  };

  const edit = (webhook: ProjectWebhook) => {
    setURL(webhook.url);
    setEventTypes(webhook.eventTypes);
    setEditingID(webhook.id);
    setDeleteCandidate(undefined);
    setNotice(undefined);
    setError(undefined);
  };

  const save = async () => {
    if (busy || !validWebhookURL(url) || eventTypes.length === 0) {
      return;
    }
    setBusy("save");
    setError(undefined);
    setNotice(undefined);
    try {
      const saved = editingID
        ? await updateProjectWebhook(projectID, editingID, { eventTypes, url })
        : await createProjectWebhook(projectID, { eventTypes, url });
      setWebhooks((current) =>
        editingID
          ? current.map((webhook) =>
              webhook.id === editingID ? saved : webhook
            )
          : [...current, saved]
      );
      resetForm();
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to save project webhook"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const test = async () => {
    if (busy || !validWebhookURL(url)) {
      return;
    }
    setBusy("test");
    setError(undefined);
    setNotice(undefined);
    try {
      await testProjectWebhook(projectID, url);
      setNotice("Test webhook delivered successfully.");
    } catch (testError) {
      setError(
        testError instanceof Error
          ? testError.message
          : "Unable to deliver test webhook"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const remove = async (webhookID: string) => {
    if (busy) {
      return;
    }
    if (deleteCandidate !== webhookID) {
      setDeleteCandidate(webhookID);
      return;
    }
    setBusy("delete");
    setError(undefined);
    try {
      await deleteProjectWebhook(projectID, webhookID);
      setWebhooks((current) =>
        current.filter((webhook) => webhook.id !== webhookID)
      );
      setDeleteCandidate(undefined);
      if (editingID === webhookID) {
        resetForm();
      }
    } catch (deleteError) {
      setError(
        deleteError instanceof Error
          ? deleteError.message
          : "Unable to delete project webhook"
      );
    } finally {
      setBusy(undefined);
    }
  };

  const formReady = validWebhookURL(url) && eventTypes.length > 0;

  return (
    <div>
      <header className="border-b border-border px-6 py-5">
        <h3 className="text-sm font-medium">Webhooks</h3>
        <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
          Send selected deployment events as JSON. Failed deliveries retry 3
          times in memory; delivery history is not retained.
        </p>
      </header>

      {error ? (
        <p className="border-b border-destructive/30 bg-destructive/5 px-6 py-3 text-[10px] leading-4 text-destructive">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="border-b border-emerald-500/30 bg-emerald-500/5 px-6 py-3 text-[10px] text-emerald-700 dark:text-emerald-300">
          {notice}
        </p>
      ) : null}

      <section className="border-b border-border">
        <div className="flex items-center justify-between px-6 py-3">
          <h4 className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
            Configured endpoints
          </h4>
          {loading ? (
            <LoaderCircle className="size-3.5 animate-spin text-muted-foreground" />
          ) : (
            <span className="text-[9px] text-muted-foreground">
              {webhooks.length}
            </span>
          )}
        </div>
        {!loading && webhooks.length === 0 ? (
          <div className="flex items-center gap-3 border-t border-border px-6 py-5 text-[10px] text-muted-foreground">
            <Webhook className="size-4" /> No webhooks configured
          </div>
        ) : null}
        {webhooks.map((webhook) => (
          <div
            className="grid min-h-14 grid-cols-[minmax(0,1fr)_auto] items-center gap-4 border-t border-border px-6 py-3"
            key={webhook.id}
          >
            <div className="min-w-0">
              <p className="truncate font-mono text-[10px]" title={webhook.url}>
                {webhook.url}
              </p>
              <p className="mt-1 text-[9px] text-muted-foreground">
                {webhook.eventTypes.length} deployment events
              </p>
            </div>
            <div className="flex items-center gap-1">
              <Button
                aria-label="Edit webhook"
                disabled={Boolean(busy)}
                onClick={() => edit(webhook)}
                size="icon"
                variant="ghost"
              >
                <Pencil />
              </Button>
              <Button
                aria-label={
                  deleteCandidate === webhook.id
                    ? "Confirm webhook deletion"
                    : "Delete webhook"
                }
                disabled={Boolean(busy)}
                onClick={() => void remove(webhook.id)}
                size={deleteCandidate === webhook.id ? "sm" : "icon"}
                variant="destructive"
              >
                {busy === "delete" && deleteCandidate === webhook.id ? (
                  <LoaderCircle className="animate-spin" />
                ) : (
                  <Trash2 />
                )}
                {deleteCandidate === webhook.id ? "Confirm" : null}
              </Button>
            </div>
          </div>
        ))}
      </section>

      <section className="px-6 py-5">
        <div className="flex items-center justify-between gap-4">
          <div>
            <h4 className="text-xs font-medium">
              {editingID ? "Edit webhook" : "New webhook"}
            </h4>
            <p className="mt-1 text-[9px] text-muted-foreground">
              HTTP redirects are not followed.
            </p>
          </div>
          {editingID ? (
            <Button onClick={resetForm} size="sm" variant="ghost">
              Cancel edit
            </Button>
          ) : null}
        </div>

        <label className="mt-5 block text-[10px]" htmlFor="project-webhook-url">
          Webhook URL
          <span className="mt-2 flex items-center border border-input focus-within:border-ring focus-within:ring-1 focus-within:ring-ring">
            <Webhook className="ml-3 size-3.5 shrink-0 text-muted-foreground" />
            <Input
              className="border-0 focus-visible:ring-0"
              id="project-webhook-url"
              onChange={(event) => {
                setURL(event.target.value);
                setNotice(undefined);
              }}
              placeholder="https://example.com/webhook"
              type="url"
              value={url}
            />
          </span>
        </label>

        <div className="mt-5">
          <p className="mb-2 text-[10px]">Event types</p>
          <WebhookEventPicker
            eventTypes={eventTypes}
            onChange={setEventTypes}
          />
        </div>

        <div className="mt-5 flex items-center justify-end gap-2 border-t border-border pt-4">
          {!validWebhookURL(url) && url ? (
            <span className="mr-auto flex items-center gap-1.5 text-[9px] text-destructive">
              <AlertTriangle className="size-3" /> Enter a valid HTTP(S) URL
            </span>
          ) : null}
          <Button
            disabled={Boolean(busy) || !validWebhookURL(url)}
            onClick={() => void test()}
            variant="outline"
          >
            {busy === "test" ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <Send />
            )}
            Test webhook
          </Button>
          <Button
            disabled={Boolean(busy) || !formReady}
            onClick={() => void save()}
          >
            {busy === "save" ? (
              <LoaderCircle className="animate-spin" />
            ) : (
              <Webhook />
            )}
            {editingID ? "Save webhook" : "Create webhook"}
          </Button>
        </div>
      </section>
    </div>
  );
};
