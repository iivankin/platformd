import { Plus, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import type { FormEvent, ReactElement, ReactNode } from "react";

import {
  analyticsFilterChip,
  analyticsFilterGroups,
  analyticsRampSteps,
  analyticsTargetingGroups,
  booleanFlagVariants,
  emptyFunnelSteps,
  isAbortError,
  matchingHostnames,
  recordRows,
  rootsOverlap,
  trackerRootCandidates,
} from "@/analytics-model";
import {
  createAnalyticsChart,
  createAnalyticsExperiment,
  createAnalyticsFlag,
  createAnalyticsFunnel,
  createAnalyticsGoal,
  createAnalyticsTracker,
  queryAnalytics,
  updateAnalyticsFlag,
  updateAnalyticsTracker,
} from "@/api";
import type {
  AnalyticsChart,
  AnalyticsExperiment,
  AnalyticsFilter,
  AnalyticsFlag,
  AnalyticsFunnel,
  AnalyticsFunnelStep,
  AnalyticsGoal,
  AnalyticsTracker,
} from "@/api";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { DateTimePicker } from "@/date-time-picker";
import { FormLabel } from "@/errors/common-ui";
import { FormFooter, Modal } from "@/errors/dialog-frame";
import { FieldSelect } from "@/field-select";

/* eslint-disable promise/prefer-await-to-then */

const anyValue = "__any__";

const FieldHint = ({ children }: { children: ReactNode }) => (
  <p className="mt-1.5 text-[10px] leading-4 text-muted-foreground">
    {children}
  </p>
);

export const TrackerDialog = ({
  hostnames,
  onSaved,
  projectID,
  trackers,
  tracker,
  trigger,
}: {
  hostnames: string[];
  onSaved: (tracker: AnalyticsTracker) => void;
  projectID: string;
  tracker?: AnalyticsTracker;
  trackers: AnalyticsTracker[];
  trigger?: ReactElement;
}) => {
  const candidates = trackerRootCandidates(hostnames);
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [name, setName] = useState(tracker?.name ?? "");
  const [rootDomain, setRootDomain] = useState(
    tracker?.rootDomain ?? candidates[0] ?? ""
  );
  const preview = matchingHostnames(rootDomain, hostnames);
  const conflict = trackers.some(
    (entry) =>
      entry.id !== tracker?.id && rootsOverlap(entry.rootDomain, rootDomain)
  );

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (conflict) {
      setError("This root overlaps another tracker.");
      return;
    }
    setPending(true);
    setError("");
    try {
      const saved = tracker
        ? await updateAnalyticsTracker(tracker.projectId, tracker.id, {
            expectedUpdatedAt: tracker.updatedAt,
            name,
            rootDomain,
          })
        : await createAnalyticsTracker(projectID, {
            name: name || rootDomain,
            rootDomain,
          });
      onSaved(saved);
      setOpen(false);
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to save tracker"
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <Modal
      description="A tracker covers one root domain and its matching hostnames. Roots must not overlap."
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) {
          return;
        }
        setError("");
        setName(tracker?.name ?? "");
        setRootDomain(tracker?.rootDomain ?? candidates[0] ?? "");
      }}
      open={open}
      title={tracker ? "Edit tracker" : "New tracker"}
      trigger={trigger}
    >
      <form onSubmit={submit}>
        <div className="grid gap-4 px-5 py-5">
          <div>
            <FormLabel htmlFor="tracker-name">Name</FormLabel>
            <Input
              id="tracker-name"
              onChange={(event) => setName(event.target.value)}
              required
              value={name}
            />
            <FieldHint>Shown only in this dashboard.</FieldHint>
          </div>
          <div>
            <FormLabel htmlFor="tracker-root">Root domain</FormLabel>
            {candidates.length === 0 ? (
              <Input
                id="tracker-root"
                onChange={(event) => {
                  const next = event.target.value.toLowerCase();
                  setRootDomain(next);
                  if (!name || name === rootDomain) {
                    setName(next);
                  }
                }}
                placeholder="localhost"
                required
                value={rootDomain}
              />
            ) : (
              <FieldSelect
                id="tracker-root"
                items={candidates.map((candidate) => ({
                  label: candidate,
                  value: candidate,
                }))}
                onValueChange={(next) => {
                  setRootDomain(next);
                  if (!name || name === rootDomain) {
                    setName(next);
                  }
                }}
                value={rootDomain}
              />
            )}
            <p className="mt-2 text-[10px] leading-4 text-muted-foreground">
              {preview.length === 0
                ? "No attached hostnames match this root yet."
                : `Matches ${preview.join(", ")}.`}
            </p>
            {conflict ? (
              <p className="mt-2 text-[10px] text-destructive">
                This root overlaps another tracker.
              </p>
            ) : null}
          </div>
        </div>
        <FormFooter
          error={error}
          label={tracker ? "Save tracker" : "Create tracker"}
          pending={pending}
        />
      </form>
    </Modal>
  );
};

export const GoalDialog = ({
  hostnames,
  onSaved,
  projectID,
  trackerID,
  trigger,
}: {
  hostnames: string[];
  onSaved: (goal: AnalyticsGoal) => void;
  projectID: string;
  trackerID: string;
  trigger?: ReactElement;
}) => {
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [actionType, setActionType] = useState<"path" | "event">("path");
  const [actionValue, setActionValue] = useState("/");
  const [hostname, setHostname] = useState("");
  const [lookup, setLookup] = useState<string[]>([]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const payload = await queryAnalytics(
          projectID,
          trackerID,
          {
            dimension: actionType === "path" ? "pathname" : "event",
            report: "lookup",
          },
          controller.signal
        );
        setLookup(
          recordRows(payload)
            .map((row) => String(row.label ?? ""))
            .filter(Boolean)
        );
      } catch (loadError) {
        if (!isAbortError(loadError)) {
          setLookup([]);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [actionType, open, projectID, trackerID]);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPending(true);
    setError("");
    try {
      const saved = await createAnalyticsGoal(projectID, trackerID, {
        actionType,
        actionValue,
        hostname: hostname || undefined,
        name,
      });
      onSaved(saved);
      setOpen(false);
      setName("");
      setActionType("path");
      setActionValue("/");
      setHostname("");
    } catch (saveError) {
      setError(
        saveError instanceof Error ? saveError.message : "Unable to save goal"
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <Modal
      description="A goal is a page view or a custom event on this tracker."
      onOpenChange={setOpen}
      open={open}
      title="New goal"
      trigger={trigger}
    >
      <form onSubmit={submit}>
        <div className="grid gap-4 px-5 py-5">
          <div>
            <FormLabel htmlFor="goal-name">Name</FormLabel>
            <Input
              id="goal-name"
              onChange={(event) => setName(event.target.value)}
              required
              value={name}
            />
            <FieldHint>Label for funnels, experiments, and reports.</FieldHint>
          </div>
          <div>
            <FormLabel htmlFor="goal-action">Action</FormLabel>
            <FieldSelect
              id="goal-action"
              items={[
                { label: "Viewed page", value: "path" },
                { label: "Triggered event", value: "event" },
              ]}
              onValueChange={(next) => setActionType(next as "path" | "event")}
              value={actionType}
            />
            <FieldHint>
              Count a matching page view or a custom event as a conversion.
            </FieldHint>
          </div>
          <div>
            <FormLabel htmlFor="goal-value">
              {actionType === "path" ? "Path" : "Event"}
            </FormLabel>
            <Input
              id="goal-value"
              list="goal-lookup"
              onChange={(event) => setActionValue(event.target.value)}
              required
              value={actionValue}
            />
            <datalist aria-hidden="true" id="goal-lookup">
              {lookup.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </datalist>
            <FieldHint>
              {actionType === "path"
                ? "Exact pathname, for example /pricing."
                : "Event name sent by the tracker, for example signup_completed."}
            </FieldHint>
          </div>
          <div>
            <FormLabel htmlFor="goal-host">Hostname pin</FormLabel>
            <FieldSelect
              id="goal-host"
              items={[
                { label: "Any hostname", value: anyValue },
                ...hostnames.map((value) => ({ label: value, value })),
              ]}
              onValueChange={(next) =>
                setHostname(next === anyValue ? "" : next)
              }
              value={hostname || anyValue}
            />
            <FieldHint>
              Leave any hostname to count this goal on every host under the
              tracker root.
            </FieldHint>
          </div>
        </div>
        <FormFooter error={error} label="Create goal" pending={pending} />
      </form>
    </Modal>
  );
};

export const FunnelDialog = ({
  hostnames,
  onSaved,
  projectID,
  trackerID,
  trigger,
}: {
  hostnames: string[];
  onSaved: (funnel: AnalyticsFunnel) => void;
  projectID: string;
  trackerID: string;
  trigger?: ReactElement;
}) => {
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [name, setName] = useState("");
  const [windowValue, setWindowValue] = useState(7);
  const [windowUnit, setWindowUnit] = useState<"minute" | "hour" | "day">(
    "day"
  );
  const [steps, setSteps] = useState<AnalyticsFunnelStep[]>(emptyFunnelSteps);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (steps.length < 2 || steps.length > 8) {
      setError("A funnel needs 2–8 steps.");
      return;
    }
    setPending(true);
    setError("");
    try {
      const saved = await createAnalyticsFunnel(projectID, trackerID, {
        name,
        steps,
        windowUnit,
        windowValue,
      });
      onSaved(saved);
      setOpen(false);
      setName("");
      setWindowValue(7);
      setWindowUnit("day");
      setSteps(emptyFunnelSteps);
    } catch (saveError) {
      setError(
        saveError instanceof Error ? saveError.message : "Unable to save funnel"
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <Modal
      className="max-w-2xl"
      description="Sequential steps on this tracker. Default window is 7 days."
      onOpenChange={setOpen}
      open={open}
      title="New funnel"
      trigger={trigger}
    >
      <form onSubmit={submit}>
        <div className="grid gap-4 px-5 py-5">
          <div>
            <FormLabel htmlFor="funnel-name">Name</FormLabel>
            <Input
              id="funnel-name"
              onChange={(event) => setName(event.target.value)}
              required
              value={name}
            />
            <FieldHint>Shown in the dashboard funnel report.</FieldHint>
          </div>
          <div className="grid grid-cols-[1fr_8rem] gap-2">
            <div>
              <FormLabel htmlFor="funnel-window">Window</FormLabel>
              <Input
                id="funnel-window"
                min={1}
                onChange={(event) =>
                  setWindowValue(Number(event.target.value) || 1)
                }
                required
                type="number"
                value={windowValue}
              />
              <FieldHint>Max time from the first step to the last.</FieldHint>
            </div>
            <div>
              <FormLabel htmlFor="funnel-unit">Unit</FormLabel>
              <FieldSelect
                id="funnel-unit"
                items={[
                  { label: "Minute", value: "minute" },
                  { label: "Hour", value: "hour" },
                  { label: "Day", value: "day" },
                ]}
                onValueChange={(next) =>
                  setWindowUnit(next as "minute" | "hour" | "day")
                }
                value={windowUnit}
              />
            </div>
          </div>
          <div className="grid gap-2">
            {steps.map((step, index) => (
              <div
                className="grid grid-cols-[7rem_1fr_9rem_2rem] gap-2"
                key={`step-${index}`}
              >
                <FieldSelect
                  aria-label={`Step ${index + 1} type`}
                  items={[
                    { label: "Path", value: "path" },
                    { label: "Event", value: "event" },
                  ]}
                  onValueChange={(next) =>
                    setSteps((current) =>
                      current.map((entry, entryIndex) =>
                        entryIndex === index
                          ? {
                              ...entry,
                              type: next as "path" | "event",
                            }
                          : entry
                      )
                    )
                  }
                  value={step.type}
                />
                <Input
                  aria-label={`Step ${index + 1} value`}
                  onChange={(event) =>
                    setSteps((current) =>
                      current.map((entry, entryIndex) =>
                        entryIndex === index
                          ? { ...entry, value: event.target.value }
                          : entry
                      )
                    )
                  }
                  required
                  value={step.value}
                />
                <FieldSelect
                  aria-label={`Step ${index + 1} hostname`}
                  items={[
                    { label: "Any host", value: anyValue },
                    ...hostnames.map((value) => ({
                      label: value,
                      value,
                    })),
                  ]}
                  onValueChange={(next) =>
                    setSteps((current) =>
                      current.map((entry, entryIndex) =>
                        entryIndex === index
                          ? {
                              ...entry,
                              hostname: next === anyValue ? undefined : next,
                            }
                          : entry
                      )
                    )
                  }
                  value={step.hostname || anyValue}
                />
                <Button
                  aria-label="Remove step"
                  disabled={steps.length <= 2}
                  onClick={() =>
                    setSteps((current) =>
                      current.filter((_, entryIndex) => entryIndex !== index)
                    )
                  }
                  size="icon"
                  type="button"
                  variant="ghost"
                >
                  <X />
                </Button>
              </div>
            ))}
            <Button
              disabled={steps.length >= 8}
              onClick={() =>
                setSteps((current) => [
                  ...current,
                  { type: "path", value: "/" },
                ])
              }
              size="sm"
              type="button"
              variant="outline"
            >
              <Plus /> Add step
            </Button>
          </div>
        </div>
        <FormFooter error={error} label="Create funnel" pending={pending} />
      </form>
    </Modal>
  );
};

interface TargetingDraft {
  groups: {
    properties: { key: string; operator: string; value: string }[];
    rollout_percentage: number;
    rollout_steps: { at: number; percentage: number }[];
    variant: string | null;
  }[];
}

const emptyTargeting = (): TargetingDraft => ({
  groups: [
    {
      properties: [],
      rollout_percentage: 100,
      rollout_steps: [],
      variant: null,
    },
  ],
});

const readTargeting = (value: unknown): TargetingDraft => {
  const groups = analyticsTargetingGroups(value);
  if (groups.length === 0) {
    return emptyTargeting();
  }
  return { groups };
};

export const FlagDialog = ({
  flag,
  onSaved,
  projectID,
  trackerID,
  trigger,
}: {
  flag?: AnalyticsFlag;
  onSaved: (flag: AnalyticsFlag) => void;
  projectID: string;
  trackerID: string;
  trigger?: ReactElement;
}) => {
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [key, setKey] = useState(flag?.key ?? "");
  const [description, setDescription] = useState(flag?.description ?? "");
  const [enabled, setEnabled] = useState(flag?.enabled ?? false);
  const [type, setType] = useState<AnalyticsFlag["type"]>(
    flag?.type ?? "boolean"
  );
  const [variants, setVariants] = useState(
    flag?.variants ?? booleanFlagVariants()
  );
  const [payload, setPayload] = useState(
    flag?.payload ? JSON.stringify(flag.payload, null, 2) : ""
  );
  const [targeting, setTargeting] = useState<TargetingDraft>(
    readTargeting(flag?.targeting)
  );
  const serveSelectValue = (variant: string | null) => {
    if (variant && variants.some((entry) => entry.key === variant)) {
      return variant;
    }
    if (type === "boolean") {
      return "true";
    }
    return anyValue;
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (type === "multivariate") {
      const weight = variants.reduce(
        (sum, variant) => sum + variant.percentage,
        0
      );
      if (Math.round(weight) !== 100) {
        setError("Variant percentages must sum to 100.");
        return;
      }
    }
    let parsedPayload: unknown = null;
    if (payload.trim()) {
      try {
        parsedPayload = JSON.parse(payload) as unknown;
      } catch {
        setError("Payload must be valid JSON.");
        return;
      }
    }
    setPending(true);
    setError("");
    try {
      const body = {
        description: description || undefined,
        enabled,
        key,
        payload: parsedPayload,
        targeting,
        type,
        variants,
      };
      const saved = flag
        ? await updateAnalyticsFlag(projectID, trackerID, flag.id, {
            ...body,
            expectedUpdatedAt: flag.updatedAt,
          })
        : await createAnalyticsFlag(projectID, trackerID, body);
      onSaved(saved);
      setOpen(false);
    } catch (saveError) {
      setError(
        saveError instanceof Error ? saveError.message : "Unable to save flag"
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <Modal
      className="max-w-2xl"
      description="OpenFeature flags evaluate with targetingKey = analytics.anonymousId()."
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) {
          return;
        }
        setError("");
        setKey(flag?.key ?? "");
        setDescription(flag?.description ?? "");
        setEnabled(flag?.enabled ?? false);
        setType(flag?.type ?? "boolean");
        setVariants(flag?.variants ?? booleanFlagVariants());
        setPayload(flag?.payload ? JSON.stringify(flag.payload, null, 2) : "");
        setTargeting(readTargeting(flag?.targeting));
      }}
      open={open}
      title={flag ? "Edit flag" : "New flag"}
      trigger={trigger}
    >
      <form onSubmit={submit}>
        <div className="grid gap-5 px-5 py-5">
          <section className="grid gap-3">
            <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              General
            </p>
            <div className="grid gap-3 sm:grid-cols-2">
              <div>
                <FormLabel htmlFor="flag-key">Key</FormLabel>
                <Input
                  id="flag-key"
                  onChange={(event) => setKey(event.target.value)}
                  required
                  value={key}
                />
                <FieldHint>
                  SDK lookup key, for example pricing-v2. Use this with
                  useFlag().
                </FieldHint>
              </div>
              <div>
                <FormLabel htmlFor="flag-description">Description</FormLabel>
                <Input
                  id="flag-description"
                  onChange={(event) => setDescription(event.target.value)}
                  value={description}
                />
                <FieldHint>Shown only in this dashboard.</FieldHint>
              </div>
            </div>
            <label
              className="flex cursor-pointer items-start gap-3"
              htmlFor="flag-enabled"
            >
              <Checkbox
                checked={enabled}
                id="flag-enabled"
                onCheckedChange={(checked) => setEnabled(checked === true)}
              />
              <span>
                <span className="block text-xs">Enabled</span>
                <span className="mt-1 block text-[10px] leading-4 text-muted-foreground">
                  Off still evaluates. Boolean serves false; multivariate serves
                  the first variant.
                </span>
              </span>
            </label>
          </section>
          <section className="grid gap-3">
            <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              Type
            </p>
            <FieldSelect
              aria-label="Flag type"
              id="flag-type"
              items={[
                { label: "Boolean", value: "boolean" },
                { label: "Multivariate", value: "multivariate" },
              ]}
              onValueChange={(next) => {
                const nextType = next as AnalyticsFlag["type"];
                setType(nextType);
                if (nextType === "boolean") {
                  setVariants(booleanFlagVariants());
                  setTargeting((current) => ({
                    groups: current.groups.map((group) => ({
                      ...group,
                      variant:
                        group.variant === "true" || group.variant === "false"
                          ? group.variant
                          : null,
                    })),
                  }));
                }
              }}
              value={type}
            />
            <FieldHint>
              Boolean is off until a visitor is in rollout, then true.
              Multivariate splits named variants among those in rollout.
            </FieldHint>
            {type === "multivariate" ? (
              <div className="grid gap-2">
                <p className="text-[10px] leading-4 text-muted-foreground">
                  Traffic split among visitors in rollout. Percentages must sum
                  to 100.
                </p>
                {variants.map((variant, index) => (
                  <div
                    className="grid grid-cols-[1fr_6rem_2rem] gap-2"
                    key={`variant-${index}`}
                  >
                    <Input
                      aria-label={`Variant ${index + 1} key`}
                      onChange={(event) =>
                        setVariants((current) =>
                          current.map((entry, entryIndex) =>
                            entryIndex === index
                              ? { ...entry, key: event.target.value }
                              : entry
                          )
                        )
                      }
                      required
                      value={variant.key}
                    />
                    <Input
                      aria-label={`Variant ${index + 1} percent`}
                      max={100}
                      min={0}
                      onChange={(event) =>
                        setVariants((current) =>
                          current.map((entry, entryIndex) =>
                            entryIndex === index
                              ? {
                                  ...entry,
                                  percentage: Number(event.target.value) || 0,
                                }
                              : entry
                          )
                        )
                      }
                      type="number"
                      value={variant.percentage}
                    />
                    <Button
                      aria-label="Remove variant"
                      disabled={variants.length <= 2}
                      onClick={() =>
                        setVariants((current) =>
                          current.filter(
                            (_, entryIndex) => entryIndex !== index
                          )
                        )
                      }
                      size="icon"
                      type="button"
                      variant="ghost"
                    >
                      <Trash2 />
                    </Button>
                  </div>
                ))}
                <Button
                  onClick={() =>
                    setVariants((current) => [
                      ...current,
                      { key: `v${current.length + 1}`, percentage: 0 },
                    ])
                  }
                  size="sm"
                  type="button"
                  variant="outline"
                >
                  <Plus /> Add variant
                </Button>
              </div>
            ) : null}
          </section>
          <section>
            <FormLabel htmlFor="flag-payload">Payload JSON</FormLabel>
            <textarea
              className="min-h-24 w-full border border-border bg-background px-2 py-2 font-mono text-[10px] outline-none"
              id="flag-payload"
              onChange={(event) => setPayload(event.target.value)}
              value={payload}
            />
            <FieldHint>
              Returned as OFREP metadata on every evaluation, including the
              default.
            </FieldHint>
          </section>
          <section className="grid gap-3">
            <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              Targeting
            </p>
            <p className="text-[10px] leading-4 text-muted-foreground">
              OR groups, first match wins. Properties come from OpenFeature
              context the app sets. Rollout is the share that leave the default;
              scheduled steps replace that share when their time arrives.
            </p>
            {targeting.groups.map((group, groupIndex) => (
              <div
                className="grid gap-2 border border-border p-3"
                key={`group-${groupIndex}`}
              >
                {group.properties.map((property, propertyIndex) => (
                  <div
                    className="grid grid-cols-[7rem_7rem_1fr_2rem] gap-2"
                    key={`prop-${groupIndex}-${propertyIndex}`}
                  >
                    <Input
                      onChange={(event) =>
                        setTargeting((current) => ({
                          groups: current.groups.map((entry, index) =>
                            index === groupIndex
                              ? {
                                  ...entry,
                                  properties: entry.properties.map(
                                    (row, rowIndex) =>
                                      rowIndex === propertyIndex
                                        ? { ...row, key: event.target.value }
                                        : row
                                  ),
                                }
                              : entry
                          ),
                        }))
                      }
                      placeholder="plan"
                      value={property.key}
                    />
                    <FieldSelect
                      items={[
                        { label: "is", value: "exact" },
                        { label: "is not", value: "is_not" },
                        { label: "contains", value: "icontains" },
                        { label: "greater than", value: "gt" },
                        { label: "less than", value: "lt" },
                        { label: "is set", value: "is_set" },
                      ]}
                      onValueChange={(next) =>
                        setTargeting((current) => ({
                          groups: current.groups.map((entry, index) =>
                            index === groupIndex
                              ? {
                                  ...entry,
                                  properties: entry.properties.map(
                                    (row, rowIndex) =>
                                      rowIndex === propertyIndex
                                        ? {
                                            ...row,
                                            operator: next,
                                          }
                                        : row
                                  ),
                                }
                              : entry
                          ),
                        }))
                      }
                      value={property.operator}
                    />
                    <Input
                      onChange={(event) =>
                        setTargeting((current) => ({
                          groups: current.groups.map((entry, index) =>
                            index === groupIndex
                              ? {
                                  ...entry,
                                  properties: entry.properties.map(
                                    (row, rowIndex) =>
                                      rowIndex === propertyIndex
                                        ? { ...row, value: event.target.value }
                                        : row
                                  ),
                                }
                              : entry
                          ),
                        }))
                      }
                      placeholder="pro"
                      value={property.value}
                    />
                    <Button
                      onClick={() =>
                        setTargeting((current) => ({
                          groups: current.groups.map((entry, index) =>
                            index === groupIndex
                              ? {
                                  ...entry,
                                  properties: entry.properties.filter(
                                    (_, rowIndex) => rowIndex !== propertyIndex
                                  ),
                                }
                              : entry
                          ),
                        }))
                      }
                      size="icon"
                      type="button"
                      variant="ghost"
                    >
                      <X />
                    </Button>
                  </div>
                ))}
                <div className="grid gap-2">
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      onClick={() =>
                        setTargeting((current) => ({
                          groups: current.groups.map((entry, index) =>
                            index === groupIndex
                              ? {
                                  ...entry,
                                  properties: [
                                    ...entry.properties,
                                    { key: "", operator: "exact", value: "" },
                                  ],
                                }
                              : entry
                          ),
                        }))
                      }
                      size="sm"
                      type="button"
                      variant="outline"
                    >
                      <Plus /> Property
                    </Button>
                    <Button
                      onClick={() =>
                        setTargeting((current) => ({
                          groups: current.groups.map((entry, index) =>
                            index === groupIndex
                              ? {
                                  ...entry,
                                  rollout_percentage: 1,
                                  rollout_steps: analyticsRampSteps(),
                                }
                              : entry
                          ),
                        }))
                      }
                      size="sm"
                      type="button"
                      variant="outline"
                    >
                      Ramp 1→100
                    </Button>
                  </div>
                  <div className="grid grid-cols-[minmax(0,1fr)_5.5rem] gap-2">
                    <div>
                      <FormLabel htmlFor={`flag-serve-${groupIndex}`}>
                        Serve
                      </FormLabel>
                      <FieldSelect
                        id={`flag-serve-${groupIndex}`}
                        items={
                          type === "boolean"
                            ? [
                                { label: "true", value: "true" },
                                { label: "false", value: "false" },
                              ]
                            : [
                                { label: "By traffic split", value: anyValue },
                                ...variants.map((variant) => ({
                                  label: variant.key,
                                  value: variant.key,
                                })),
                              ]
                        }
                        onValueChange={(next) =>
                          setTargeting((current) => ({
                            groups: current.groups.map((entry, index) =>
                              index === groupIndex
                                ? {
                                    ...entry,
                                    variant:
                                      next === anyValue || next === ""
                                        ? null
                                        : next,
                                  }
                                : entry
                            ),
                          }))
                        }
                        value={serveSelectValue(group.variant)}
                      />
                    </div>
                    <div>
                      <FormLabel htmlFor={`flag-rollout-${groupIndex}`}>
                        Now %
                      </FormLabel>
                      <Input
                        id={`flag-rollout-${groupIndex}`}
                        max={100}
                        min={0}
                        onChange={(event) =>
                          setTargeting((current) => ({
                            groups: current.groups.map((entry, index) =>
                              index === groupIndex
                                ? {
                                    ...entry,
                                    rollout_percentage:
                                      Number(event.target.value) || 0,
                                  }
                                : entry
                            ),
                          }))
                        }
                        type="number"
                        value={group.rollout_percentage}
                      />
                    </div>
                  </div>
                  {group.rollout_steps.map((step, stepIndex) => (
                    <div
                      className="grid grid-cols-[minmax(0,1fr)_5.5rem_2rem] gap-2"
                      key={`ramp-${groupIndex}-${step.at}-${stepIndex}`}
                    >
                      <DateTimePicker
                        onChange={(next) =>
                          setTargeting((current) => ({
                            groups: current.groups.map((entry, index) =>
                              index === groupIndex
                                ? {
                                    ...entry,
                                    rollout_steps: entry.rollout_steps.map(
                                      (row, rowIndex) =>
                                        rowIndex === stepIndex
                                          ? { ...row, at: next }
                                          : row
                                    ),
                                  }
                                : entry
                            ),
                          }))
                        }
                        value={step.at}
                      />
                      <Input
                        aria-label={`Ramp step ${stepIndex + 1} percent`}
                        max={100}
                        min={0}
                        onChange={(event) =>
                          setTargeting((current) => ({
                            groups: current.groups.map((entry, index) =>
                              index === groupIndex
                                ? {
                                    ...entry,
                                    rollout_steps: entry.rollout_steps.map(
                                      (row, rowIndex) =>
                                        rowIndex === stepIndex
                                          ? {
                                              ...row,
                                              percentage:
                                                Number(event.target.value) || 0,
                                            }
                                          : row
                                    ),
                                  }
                                : entry
                            ),
                          }))
                        }
                        type="number"
                        value={step.percentage}
                      />
                      <Button
                        aria-label="Remove ramp step"
                        onClick={() =>
                          setTargeting((current) => ({
                            groups: current.groups.map((entry, index) =>
                              index === groupIndex
                                ? {
                                    ...entry,
                                    rollout_steps: entry.rollout_steps.filter(
                                      (_, rowIndex) => rowIndex !== stepIndex
                                    ),
                                  }
                                : entry
                            ),
                          }))
                        }
                        size="icon"
                        type="button"
                        variant="ghost"
                      >
                        <X />
                      </Button>
                    </div>
                  ))}
                  <Button
                    onClick={() =>
                      setTargeting((current) => ({
                        groups: current.groups.map((entry, index) =>
                          index === groupIndex
                            ? {
                                ...entry,
                                rollout_steps: [
                                  ...entry.rollout_steps,
                                  {
                                    at: Date.now() + 86_400_000,
                                    percentage: Math.min(
                                      100,
                                      Math.max(entry.rollout_percentage, 10)
                                    ),
                                  },
                                ],
                              }
                            : entry
                        ),
                      }))
                    }
                    size="sm"
                    type="button"
                    variant="outline"
                  >
                    <Plus /> Schedule step
                  </Button>
                </div>
              </div>
            ))}
            <Button
              onClick={() =>
                setTargeting((current) => ({
                  groups: [
                    ...current.groups,
                    {
                      properties: [],
                      rollout_percentage: 100,
                      rollout_steps: [],
                      variant: null,
                    },
                  ],
                }))
              }
              size="sm"
              type="button"
              variant="outline"
            >
              <Plus /> OR group
            </Button>
          </section>
        </div>
        <FormFooter
          error={error}
          label={flag ? "Save flag" : "Create flag"}
          pending={pending}
        />
      </form>
    </Modal>
  );
};

export const ExperimentDialog = ({
  flags,
  goals,
  onSaved,
  projectID,
  trackerID,
  trigger,
}: {
  flags: AnalyticsFlag[];
  goals: AnalyticsGoal[];
  onSaved: (experiment: AnalyticsExperiment) => void;
  projectID: string;
  trackerID: string;
  trigger?: ReactElement;
}) => {
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [flagID, setFlagID] = useState(flags[0]?.id ?? "");
  const selected = flags.find((flag) => flag.id === flagID);
  const [controlVariant, setControlVariant] = useState(
    selected?.type === "boolean" ? "false" : (selected?.variants[0]?.key ?? "")
  );
  const [metric, setMetric] = useState(
    goals[0]?.id ? `goal:${goals[0].id}` : "event:signup_completed"
  );
  const [windowValue, setWindowValue] = useState(14);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPending(true);
    setError("");
    try {
      const saved = await createAnalyticsExperiment(projectID, trackerID, {
        controlVariant,
        flagId: flagID,
        metric: metric.startsWith("goal:")
          ? { goalId: metric.slice(5) }
          : { eventName: metric.replace(/^event:/u, "") },
        windowUnit: "day",
        windowValue,
      });
      onSaved(saved);
      setOpen(false);
    } catch (saveError) {
      setError(
        saveError instanceof Error
          ? saveError.message
          : "Unable to start experiment"
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <Modal
      description="Exposure is the first $flag_called after start. Conversion is the primary metric after that, with a Wilson interval."
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) {
          return;
        }
        setError("");
        setFlagID(flags[0]?.id ?? "");
        setControlVariant(
          flags[0]?.type === "boolean"
            ? "false"
            : (flags[0]?.variants[0]?.key ?? "")
        );
        setMetric(
          goals[0]?.id ? `goal:${goals[0].id}` : "event:signup_completed"
        );
        setWindowValue(14);
      }}
      open={open}
      title="Start experiment"
      trigger={trigger}
    >
      <form onSubmit={submit}>
        <div className="grid gap-4 px-5 py-5">
          <div>
            <FormLabel htmlFor="experiment-flag">Flag</FormLabel>
            <FieldSelect
              id="experiment-flag"
              items={flags.map((flag) => ({
                label: flag.key,
                value: flag.id,
              }))}
              onValueChange={(next) => {
                setFlagID(next);
                const nextFlag = flags.find((flag) => flag.id === next);
                setControlVariant(
                  nextFlag?.type === "boolean"
                    ? "false"
                    : (nextFlag?.variants[0]?.key ?? "")
                );
              }}
              value={flagID}
            />
            <FieldHint>
              Variants on this flag are the arms of the test.
            </FieldHint>
          </div>
          <div>
            <FormLabel htmlFor="experiment-control">Control variant</FormLabel>
            <FieldSelect
              id="experiment-control"
              items={(selected?.variants ?? []).map((variant) => ({
                label: variant.key,
                value: variant.key,
              }))}
              onValueChange={setControlVariant}
              value={controlVariant}
            />
            <FieldHint>
              Baseline for lift. Usually false, or the current default.
            </FieldHint>
          </div>
          <div>
            <FormLabel htmlFor="experiment-metric">Primary metric</FormLabel>
            <FieldSelect
              id="experiment-metric"
              items={[
                ...goals.map((goal) => ({
                  label: `Goal · ${goal.name}`,
                  value: `goal:${goal.id}`,
                })),
                {
                  label: "Event · signup_completed",
                  value: "event:signup_completed",
                },
              ]}
              onValueChange={setMetric}
              value={metric}
            />
            <FieldHint>
              Counted as a conversion after the visitor is first exposed to the
              flag.
            </FieldHint>
          </div>
          <div>
            <FormLabel htmlFor="experiment-window">Window (days)</FormLabel>
            <Input
              id="experiment-window"
              min={1}
              onChange={(event) =>
                setWindowValue(Number(event.target.value) || 14)
              }
              type="number"
              value={windowValue}
            />
            <FieldHint>
              How long a visitor can convert after first exposure.
            </FieldHint>
          </div>
          <p className="text-[10px] leading-4 text-muted-foreground">
            Do not change variant weights while this experiment is running.
          </p>
        </div>
        <FormFooter error={error} label="Start experiment" pending={pending} />
      </form>
    </Modal>
  );
};

export const AnalyticsFilterPopover = ({
  filters,
  onChange,
  projectID,
  trackerID,
}: {
  filters: AnalyticsFilter[];
  onChange: (filters: AnalyticsFilter[]) => void;
  projectID: string;
  trackerID: string;
}) => {
  const [open, setOpen] = useState(false);
  const [dimension, setDimension] = useState("page");
  const [operator, setOperator] = useState<AnalyticsFilter["operator"]>("is");
  const [value, setValue] = useState("");
  const [lookup, setLookup] = useState<string[]>([]);
  const lookupDimension = useMemo(() => {
    if (dimension === "entry_page" || dimension === "exit_page") {
      return "page";
    }
    return dimension;
  }, [dimension]);

  useEffect(() => {
    if (
      !(open && lookupDimension && (operator === "is" || operator === "is_not"))
    ) {
      return;
    }
    const controller = new AbortController();
    const load = async () => {
      try {
        const payload = await queryAnalytics(
          projectID,
          trackerID,
          { dimension: lookupDimension, report: "lookup" },
          controller.signal
        );
        setLookup(
          recordRows(payload)
            .map((row) => String(row.label ?? ""))
            .filter(Boolean)
        );
      } catch (loadError) {
        if (!isAbortError(loadError)) {
          setLookup([]);
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [lookupDimension, open, operator, projectID, trackerID]);

  return (
    <div className="relative">
      <Button
        onClick={() => setOpen((current) => !current)}
        size="sm"
        variant="ghost"
      >
        Filter
      </Button>
      {open ? (
        <div className="absolute top-full right-0 z-40 mt-1 w-[28rem] border border-border bg-background p-3 shadow-md">
          <div className="grid grid-cols-2 gap-4">
            {analyticsFilterGroups.map((group) => (
              <div key={group.label}>
                <p className="mb-1 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                  {group.label}
                </p>
                <div className="grid gap-0.5">
                  {group.dimensions.map((entry) => (
                    <button
                      className={`h-7 px-2 text-left text-[10px] hover:bg-muted ${
                        dimension === entry.dimension ? "bg-muted" : ""
                      }`}
                      key={entry.dimension}
                      onClick={() => setDimension(entry.dimension)}
                      type="button"
                    >
                      {entry.label}
                    </button>
                  ))}
                </div>
              </div>
            ))}
          </div>
          <div className="mt-3 grid grid-cols-[7rem_1fr] gap-2">
            <FieldSelect
              aria-label="Filter operator"
              items={[
                { label: "is", value: "is" },
                { label: "is not", value: "is_not" },
                { label: "contains", value: "contains" },
                { label: "does not contain", value: "does_not_contain" },
              ]}
              onValueChange={(next) =>
                setOperator(next as AnalyticsFilter["operator"])
              }
              value={operator}
            />
            <Input
              aria-label="Filter value"
              list="analytics-filter-lookup"
              onChange={(event) => setValue(event.target.value)}
              placeholder="Value"
              value={value}
            />
            <datalist aria-hidden="true" id="analytics-filter-lookup">
              {lookup.map((entry) => (
                <option key={entry} value={entry}>
                  {entry}
                </option>
              ))}
            </datalist>
          </div>
          <div className="mt-3 flex justify-end">
            <Button
              disabled={!value || filters.length >= 8}
              onClick={() => {
                onChange([...filters, { dimension, operator, value }]);
                setValue("");
                setOpen(false);
              }}
              size="sm"
            >
              Apply
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  );
};

export const AnalyticsFilterChips = ({
  filters,
  onRemove,
}: {
  filters: AnalyticsFilter[];
  onRemove: (index: number) => void;
}) => (
  <div className="flex min-w-0 flex-wrap items-center gap-2">
    {filters.map((filter, index) => (
      <span
        className="flex h-8 max-w-56 shrink-0 items-center gap-1 border border-border px-2 text-[8px] text-muted-foreground"
        key={`${filter.dimension}:${filter.operator}:${index}`}
      >
        <span className="truncate">{analyticsFilterChip(filter)}</span>
        <button
          aria-label="Remove filter"
          className="hover:text-foreground"
          onClick={() => onRemove(index)}
          type="button"
        >
          <X className="size-2.5" />
        </button>
      </span>
    ))}
  </div>
);

const analyticsSqlColumns = [
  ["time", "UInt64", "Event time in milliseconds"],
  ["value", "Float64", "Chart value"],
  ["series", "String", "Optional series key"],
  ["event_name", "String", "Event name"],
  ["hostname", "String", "Request hostname"],
  ["pathname", "String", "Path"],
  ["referrer_source", "String", "Source"],
  ["channel", "String", "Channel"],
  ["country", "String", "Country"],
  ["device", "String", "Device"],
];

export const AnalyticsChartDialog = ({
  onSaved,
  projectID,
  trackerID,
  trigger,
}: {
  onSaved: (chart: AnalyticsChart) => void;
  projectID: string;
  trackerID: string;
  trigger?: ReactElement;
}) => {
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [title, setTitle] = useState("");
  const [sql, setSql] = useState(
    "SELECT time, count() AS value FROM analytics GROUP BY time ORDER BY time"
  );
  const [visualization, setVisualization] =
    useState<AnalyticsChart["visualization"]>("area");
  const [preview, setPreview] = useState<Record<string, unknown>[]>([]);
  const [previewError, setPreviewError] = useState("");

  const runPreview = async () => {
    setPreviewError("");
    try {
      const payload = await queryAnalytics(projectID, trackerID, {
        report: "sql",
        sql,
      });
      setPreview(recordRows(payload));
      return true;
    } catch (queryError) {
      setPreview([]);
      setPreviewError(
        queryError instanceof Error ? queryError.message : "Preview failed"
      );
      return false;
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setPending(true);
    setError("");
    try {
      if (!(await runPreview())) {
        return;
      }
      const saved = await createAnalyticsChart(projectID, trackerID, {
        legend: title,
        sql,
        title,
        visualization,
      });
      onSaved(saved);
      setOpen(false);
      setTitle("");
      setSql(
        "SELECT time, count() AS value FROM analytics GROUP BY time ORDER BY time"
      );
      setVisualization("area");
      setPreview([]);
      setPreviewError("");
    } catch (saveError) {
      setError(
        saveError instanceof Error ? saveError.message : "Unable to save chart"
      );
    } finally {
      setPending(false);
    }
  };

  return (
    <Modal
      className="max-w-6xl"
      description="Restricted SELECT over the virtual analytics table. Return time and value, with optional series."
      onOpenChange={setOpen}
      open={open}
      title="New analytics chart"
      trigger={trigger}
    >
      <form onSubmit={submit}>
        <div className="grid gap-4 px-5 py-5 lg:grid-cols-[minmax(0,1fr)_18rem]">
          <div className="grid gap-4">
            <div className="grid gap-4 sm:grid-cols-[1fr_12rem]">
              <div>
                <FormLabel htmlFor="analytics-chart-title">Title</FormLabel>
                <Input
                  id="analytics-chart-title"
                  onChange={(event) => setTitle(event.target.value)}
                  required
                  value={title}
                />
              </div>
              <div>
                <FormLabel htmlFor="analytics-chart-viz">
                  Visualization
                </FormLabel>
                <FieldSelect
                  id="analytics-chart-viz"
                  items={[
                    { label: "Area", value: "area" },
                    { label: "Line", value: "line" },
                    { label: "Bar", value: "bar" },
                    { label: "Value", value: "value" },
                    { label: "Table", value: "table" },
                  ]}
                  onValueChange={(next) =>
                    setVisualization(next as AnalyticsChart["visualization"])
                  }
                  value={visualization}
                />
                <FieldHint>
                  Area, line, and bar need time and value. Value is a single
                  number. Table lists rows.
                </FieldHint>
              </div>
            </div>
            <textarea
              className="min-h-72 w-full border border-border bg-background px-3 py-3 font-mono text-[10px] outline-none"
              onChange={(event) => setSql(event.target.value)}
              required
              spellCheck={false}
              value={sql}
            />
            {previewError ? (
              <p className="text-[10px] text-destructive">{previewError}</p>
            ) : null}
            {preview.length > 0 ? (
              <p className="text-[10px] text-muted-foreground">
                Preview · {preview.length} rows
              </p>
            ) : null}
          </div>
          <aside className="border-l border-border pl-4">
            <p className="text-[9px] tracking-[0.12em] text-muted-foreground uppercase">
              analytics
            </p>
            <div className="mt-3 grid gap-2">
              {analyticsSqlColumns.map(([column, type, copy]) => (
                <div key={column}>
                  <p className="text-[10px]">
                    {column}{" "}
                    <span className="text-muted-foreground">{type}</span>
                  </p>
                  <p className="text-[9px] text-muted-foreground">{copy}</p>
                </div>
              ))}
            </div>
            <Button
              className="mt-4"
              onClick={() => void runPreview()}
              size="sm"
              type="button"
              variant="outline"
            >
              Preview
            </Button>
          </aside>
        </div>
        <FormFooter error={error} label="Add chart" pending={pending} />
      </form>
    </Modal>
  );
};
