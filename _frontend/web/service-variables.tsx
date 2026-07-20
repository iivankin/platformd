import { Menu } from "@base-ui/react/menu";
import {
  Braces,
  Check,
  Copy,
  MoreVertical,
  Plus,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import {
  fetchProjectCanvas,
  fetchResolvedServiceEnvironment,
  fetchService,
  fetchServiceDomains,
} from "@/api";
import type { ProjectCanvas, Service, ServiceDomain } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import {
  VariableNameCombobox,
  VariableValueCombobox,
} from "@/service-variable-combobox";
import {
  environmentName,
  serviceVariableRows,
  variableSuggestions,
} from "@/service-variable-model";
import type { VariableRow } from "@/service-variable-model";

const rawEnvironment = (environment: Record<string, string>) =>
  Object.entries(environment)
    .toSorted(([left], [right]) => left.localeCompare(right))
    .map(([name, value]) => `${name}=${value}`)
    .join("\n");
export const ServiceVariables = ({
  busy,
  onSave,
  projectID,
  resolvedRaw = true,
  service,
}: {
  busy: boolean;
  onSave: (environment: Record<string, string>) => Promise<boolean>;
  projectID: string;
  resolvedRaw?: boolean;
  service: Pick<Service, "environment" | "id">;
}) => {
  const [rows, setRows] = useState<VariableRow[]>(() =>
    serviceVariableRows(service)
  );
  const [resources, setResources] = useState<ProjectCanvas["resources"]>([]);
  const [services, setServices] = useState<Map<string, Service>>(new Map());
  const [domains, setDomains] = useState<Map<string, ServiceDomain[]>>(
    new Map()
  );
  const [raw, setRaw] = useState<string>();
  const [resolving, setResolving] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const loadResources = async () => {
      try {
        const canvas = await fetchProjectCanvas(projectID, controller.signal);
        const available = canvas.resources;
        setResources(available);
        const serviceResources = available.filter(
          (resource) => resource.kind === "service"
        );
        const loaded = await Promise.all(
          serviceResources.map(async (resource) => ({
            domains: await fetchServiceDomains(
              projectID,
              resource.id,
              controller.signal
            ),
            service: await fetchService(
              projectID,
              resource.id,
              controller.signal
            ),
          }))
        );
        setServices(
          new Map(loaded.map((entry) => [entry.service.id, entry.service]))
        );
        setDomains(
          new Map(
            loaded.map((entry) => [entry.service.id, entry.domains] as const)
          )
        );
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
            : "Unable to load variable suggestions"
        );
      }
    };
    void loadResources();
    return () => controller.abort();
  }, [projectID, service.id]);

  const suggestions = useMemo(
    () => variableSuggestions(resources, services, domains, service.id),
    [domains, resources, service.id, services]
  );

  const updateRow = (rowID: string, update: Partial<VariableRow>) => {
    setRows((current) =>
      current.map((row) => (row.id === rowID ? { ...row, ...update } : row))
    );
    setRaw(undefined);
  };

  const addVariable = () => {
    setRows((current) => [
      { id: crypto.randomUUID(), name: "", value: "" },
      ...current,
    ]);
    setRaw(undefined);
  };

  const save = async () => {
    const environment: Record<string, string> = {};
    for (const row of rows) {
      if (!environmentName.test(row.name)) {
        setError(`Invalid environment name: ${row.name || "(empty)"}`);
        return;
      }
      if (row.name in environment) {
        setError(`Duplicate environment name: ${row.name}`);
        return;
      }
      environment[row.name] = row.value;
    }
    if (await onSave(environment)) {
      setError(undefined);
      setRaw(undefined);
    }
  };

  const resolveRaw = async () => {
    setResolving(true);
    setError(undefined);
    try {
      setRaw(
        rawEnvironment(
          await fetchResolvedServiceEnvironment(projectID, service.id)
        )
      );
    } catch (resolveError) {
      setError(
        resolveError instanceof Error
          ? resolveError.message
          : "Unable to resolve variables"
      );
    } finally {
      setResolving(false);
    }
  };

  const toggleRaw = async () => {
    if (raw === undefined) {
      await resolveRaw();
      return;
    }
    setRaw(undefined);
  };
  const copyRaw = async () => {
    await navigator.clipboard.writeText(raw ?? "");
  };
  let rawButtonLabel = "Rows";
  if (resolving) {
    rawButtonLabel = "Resolving…";
  } else if (raw === undefined) {
    rawButtonLabel = "Resolved raw";
  }

  return (
    <SectionCard>
      <header className="flex min-h-16 items-center justify-between gap-4 bg-muted/25 px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">
            {rows.length} service variables
          </h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            References are ordinary values resolved when a deployment starts.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {resolvedRaw ? (
            <Button
              disabled={busy || resolving}
              onClick={toggleRaw}
              size="sm"
              variant="ghost"
            >
              <Braces /> {rawButtonLabel}
            </Button>
          ) : null}
          <Button onClick={addVariable} size="sm" variant="outline">
            <Plus /> New variable
          </Button>
        </div>
      </header>

      {raw === undefined ? (
        <>
          <div className="grid grid-cols-[minmax(11rem,0.8fr)_minmax(16rem,1.2fr)_2.5rem] border-y border-border bg-muted/10 px-5 py-2 text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
            <span>Name</span>
            <span>Value</span>
            <span />
          </div>

          {rows.length ? (
            rows.map((row) => (
              <div
                className="grid min-h-12 grid-cols-[minmax(11rem,0.8fr)_minmax(16rem,1.2fr)_2.5rem] border-b border-border last:border-b-0"
                key={row.id}
              >
                <div className="min-w-0 border-r border-border">
                  <VariableNameCombobox
                    busy={busy}
                    onChange={(name) => updateRow(row.id, { name })}
                    onSelect={(suggestion) =>
                      updateRow(row.id, {
                        name: suggestion.variableName,
                        value: suggestion.expression,
                      })
                    }
                    row={row}
                    suggestions={suggestions}
                  />
                </div>

                <VariableValueCombobox
                  busy={busy}
                  onChange={(value) => updateRow(row.id, { value })}
                  row={row}
                  suggestions={suggestions}
                />

                <Menu.Root>
                  <Menu.Trigger
                    aria-label={`Actions for ${row.name || "variable"}`}
                    className="grid h-full min-h-12 place-items-center text-muted-foreground hover:bg-muted hover:text-foreground"
                    disabled={busy}
                  >
                    <MoreVertical className="size-3.5" />
                  </Menu.Trigger>
                  <Menu.Portal>
                    <Menu.Positioner
                      align="end"
                      className="z-50"
                      sideOffset={4}
                    >
                      <Menu.Popup className="min-w-44 border border-border bg-popover p-1 text-[10px] text-popover-foreground shadow-lg">
                        <Menu.Item
                          className="flex cursor-default items-center gap-2 px-2.5 py-2 text-destructive outline-none data-[highlighted]:bg-destructive/10"
                          onClick={() => {
                            setRows((current) =>
                              current.filter(
                                (candidate) => candidate.id !== row.id
                              )
                            );
                            setRaw(undefined);
                          }}
                        >
                          <Trash2 className="size-3.5" /> Remove
                        </Menu.Item>
                      </Menu.Popup>
                    </Menu.Positioner>
                  </Menu.Portal>
                </Menu.Root>
              </div>
            ))
          ) : (
            <p className="border-b border-dashed border-border px-5 py-6 text-[10px] text-muted-foreground">
              No variables configured.
            </p>
          )}
        </>
      ) : (
        <div className="border-t border-border">
          <div className="flex min-h-10 items-center border-b border-border bg-muted/10 px-5 text-[9px] text-muted-foreground">
            Resolved deployment values
            <Button
              aria-label="Copy resolved variables"
              className="ml-auto"
              onClick={copyRaw}
              size="icon"
              variant="ghost"
            >
              <Copy />
            </Button>
          </div>
          <pre className="min-h-64 overflow-auto px-5 py-4 text-[10px] leading-5 break-all whitespace-pre-wrap">
            {raw || "No variables configured."}
          </pre>
        </div>
      )}

      <footer className="flex min-h-14 items-center justify-end gap-3 border-t border-border bg-muted/15 px-5 py-3">
        {error ? (
          <p className="mr-auto text-[10px] text-destructive">{error}</p>
        ) : null}
        {raw === undefined ? (
          <Button disabled={busy} onClick={() => void save()}>
            <Check /> {busy ? "Saving…" : "Save variables"}
          </Button>
        ) : (
          <Button onClick={() => setRaw(undefined)} variant="outline">
            <X /> Close raw view
          </Button>
        )}
      </footer>
    </SectionCard>
  );
};
