import { RadioTower } from "lucide-react";
import { useState } from "react";
import type { FormEvent } from "react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";

import { api } from "./api";
import { FormLabel } from "./common-ui";
import { FormFooter, Modal, SecretReveal } from "./dialog-frame";
import { errorMessage } from "./format";
import type { WebhookEvent } from "./types";

const webhookEvents: { label: string; value: WebhookEvent }[] = [
  { label: "Event received", value: "event_received" },
  { label: "Issue created", value: "issue_created" },
  { label: "Issue regressed", value: "issue_regressed" },
  { label: "Issue resolved", value: "issue_resolved" },
];

export const CreateWebhookDialog = ({
  appId,
  onCreated,
}: {
  appId: string;
  onCreated: () => Promise<void>;
}) => {
  const defaultEvents: WebhookEvent[] = ["event_received", "issue_created"];
  const [open, setOpen] = useState(false);
  const [url, setUrl] = useState("");
  const [events, setEvents] = useState<WebhookEvent[]>(defaultEvents);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [secret, setSecret] = useState("");

  const reset = () => {
    setUrl("");
    setEvents(defaultEvents);
    setError("");
    setSecret("");
  };
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (events.length === 0) {
      setError("Select at least one event");
      return;
    }
    setPending(true);
    setError("");
    try {
      const created = await api.createWebhook(appId, url.trim(), events);
      setSecret(created.secret);
      await onCreated();
    } catch (createError) {
      setError(errorMessage(createError, "Unable to create webhook"));
    } finally {
      setPending(false);
    }
  };

  return (
    <>
      <Button onClick={() => setOpen(true)} size="sm">
        <RadioTower /> Add webhook
      </Button>
      <Modal
        description="The signing secret is generated once and must be stored before closing."
        onOpenChange={(nextOpen) => {
          setOpen(nextOpen);
          if (!nextOpen) {
            reset();
          }
        }}
        open={open}
        title="Create webhook"
      >
        {secret ? (
          <SecretReveal values={[{ label: "Signing secret", value: secret }]} />
        ) : (
          <form onSubmit={submit}>
            <div className="space-y-4 px-5 py-5">
              <div>
                <FormLabel htmlFor="webhook-url">Endpoint URL</FormLabel>
                <Input
                  autoFocus
                  id="webhook-url"
                  onChange={(event) => setUrl(event.target.value)}
                  placeholder="https://hooks.example.com/errors"
                  required
                  type="url"
                  value={url}
                />
              </div>
              <fieldset>
                <legend className="mb-2 text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
                  Events
                </legend>
                <div className="grid gap-px border border-border bg-border sm:grid-cols-2">
                  {webhookEvents.map((item) => (
                    <label
                      className="flex items-center gap-2 bg-background px-3 py-2.5 text-[10px]"
                      key={item.value}
                    >
                      <Checkbox
                        checked={events.includes(item.value)}
                        onCheckedChange={(checked) => {
                          setEvents((current) =>
                            checked
                              ? [...current, item.value]
                              : current.filter((value) => value !== item.value)
                          );
                        }}
                      />
                      {item.label}
                    </label>
                  ))}
                </div>
              </fieldset>
            </div>
            <FormFooter
              error={error}
              label="Create webhook"
              pending={pending}
            />
          </form>
        )}
      </Modal>
    </>
  );
};
