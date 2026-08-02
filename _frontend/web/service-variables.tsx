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
  fetchResolvedServiceBuildEnvironment,
  fetchResolvedServiceEnvironment,
  fetchService,
  fetchServiceDomains,
} from "@/api";
import type { ProjectCanvas, Service, ServiceDomain } from "@/api";
import { Button } from "@/components/ui/button";
import { SectionCard } from "@/components/ui/card";
import { newID } from "@/id";
import { mergePendingCanvasResources } from "@/pending-resource-creation";
import { useProjectChanges } from "@/project-changes";
import { ServiceSystemVariablesDialog } from "@/service-system-variables-dialog";
import {
  VariableNameCombobox,
  VariableValueCombobox,
} from "@/service-variable-combobox";
import {
  environmentName,
  serviceVariableRows,
  variableSuggestions,
} from "@/service-variable-model";
import type { VariableRow, VariableSuggestion } from "@/service-variable-model";

interface ServiceVariableUpdate {
  buildEnvironment?: Record<string, string>;
  environment?: Record<string, string>;
}

type VariableScope = "build" | "runtime";

const rawEnvironment = (environment: Record<string, string>) =>
  Object.entries(environment)
    .toSorted(([left], [right]) => left.localeCompare(right))
    .map(([name, value]) => `${name}=${value}`)
    .join("\n");

const VariableSection = ({
  busy,
  environment,
  onSave,
  resolve,
  scope,
  suggestions,
  suggestionsError,
}: {
  busy: boolean;
  environment: Record<string, string>;
  onSave: (environment: Record<string, string>) => Promise<boolean>;
  resolve?: () => Promise<Record<string, string>>;
  scope: VariableScope;
  suggestions: VariableSuggestion[];
  suggestionsError?: string;
}) => {
  const [rows, setRows] = useState<VariableRow[]>(() =>
    serviceVariableRows({ environment })
  );
  const [raw, setRaw] = useState<string>();
  const [resolving, setResolving] = useState(false);
  const [error, setError] = useState<string>();
  const build = scope === "build";

  const updateRow = (rowID: string, update: Partial<VariableRow>) => {
    setRows((current) =>
      current.map((row) => (row.id === rowID ? { ...row, ...update } : row))
    );
    setRaw(undefined);
  };

  const addVariable = () => {
    setRows((current) => [{ id: newID(), name: "", value: "" }, ...current]);
    setRaw(undefined);
  };

  const save = async () => {
    const entries: [string, string][] = [];
    const names = new Set<string>();
    for (const row of rows) {
      if (!environmentName.test(row.name)) {
        setError(`Invalid environment name: ${row.name || "(empty)"}`);
        return;
      }
      if (names.has(row.name)) {
        setError(`Duplicate environment name: ${row.name}`);
        return;
      }
      names.add(row.name);
      entries.push([row.name, row.value]);
    }
    if (await onSave(Object.fromEntries(entries))) {
      setError(undefined);
      setRaw(undefined);
    }
  };

  const toggleRaw = async () => {
    if (raw !== undefined) {
      setRaw(undefined);
      return;
    }
    if (!resolve) {
      return;
    }
    setResolving(true);
    setError(undefined);
    try {
      setRaw(rawEnvironment(await resolve()));
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

  let rawButtonLabel = "Rows";
  if (resolving) {
    rawButtonLabel = "Resolving…";
  } else if (raw === undefined) {
    rawButtonLabel = "Resolved raw";
  }
  const visibleError = error ?? suggestionsError;

  return (
    <SectionCard>
      <header className="flex min-h-16 items-center justify-between gap-4 bg-muted/25 px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">
            {rows.length} {build ? "build time" : "service"} variables
          </h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            {build
              ? "Stable build defaults and preview context are added automatically. Values are available to every Dockerfile step and remain in the built image environment."
              : "Deployment, URL, Git, and preview context are added automatically. References resolve when a deployment starts."}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {resolve ? (
            <Button
              disabled={busy || resolving}
              onClick={() => void toggleRaw()}
              size="sm"
              variant="ghost"
            >
              <Braces /> {rawButtonLabel}
            </Button>
          ) : null}
          <Button
            disabled={busy}
            onClick={addVariable}
            size="sm"
            variant="outline"
          >
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
              No {build ? "build time " : ""}variables configured.
            </p>
          )}
        </>
      ) : (
        <div className="border-t border-border">
          <div className="flex min-h-10 items-center border-b border-border bg-muted/10 px-5 text-[9px] text-muted-foreground">
            Resolved {build ? "build" : "deployment"} values
            <Button
              aria-label="Copy resolved variables"
              className="ml-auto"
              onClick={() => void navigator.clipboard.writeText(raw)}
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
        {visibleError ? (
          <p className="mr-auto text-[10px] text-destructive">{visibleError}</p>
        ) : null}
        {raw === undefined ? (
          <Button disabled={busy} onClick={() => void save()}>
            <Check />{" "}
            {busy ? "Staging…" : `Stage ${build ? "build " : ""}variables`}
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

export const ServiceVariables = ({
  busy,
  onSave,
  projectID,
  resolvedRaw = true,
  service,
}: {
  busy: boolean;
  onSave: (update: ServiceVariableUpdate) => Promise<boolean>;
  projectID: string;
  resolvedRaw?: boolean;
  service: Pick<Service, "buildEnvironment" | "environment" | "id" | "source">;
}) => {
  const { resourceDrafts, serviceChanges } = useProjectChanges(projectID);
  const [resources, setResources] = useState<ProjectCanvas["resources"]>([]);
  const [projectName, setProjectName] = useState("");
  const [services, setServices] = useState<Map<string, Service>>(new Map());
  const [domains, setDomains] = useState<Map<string, ServiceDomain[]>>(
    new Map()
  );
  const [suggestionsError, setSuggestionsError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const loadResources = async () => {
      setSuggestionsError(undefined);
      try {
        const canvas = await fetchProjectCanvas(projectID, controller.signal);
        const available = canvas.resources;
        setResources(available);
        setProjectName(canvas.project.name);
        const serviceResources = available.filter(
          (resource) => resource.kind === "service"
        );
        const loaded = await Promise.all(
          serviceResources.map(async (resource) => {
            const [loadedDomains, loadedService] = await Promise.all([
              fetchServiceDomains(projectID, resource.id, controller.signal),
              fetchService(projectID, resource.id, controller.signal),
            ]);
            return { domains: loadedDomains, service: loadedService };
          })
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
        setSuggestionsError(
          loadError instanceof Error
            ? loadError.message
            : "Unable to load variable suggestions"
        );
      }
    };
    void loadResources();
    return () => controller.abort();
  }, [projectID, service.id]);

  const drafts = useMemo(() => Object.values(resourceDrafts), [resourceDrafts]);
  const resourcesWithDrafts = useMemo(
    () =>
      mergePendingCanvasResources(resources, drafts, projectName, new Set()),
    [drafts, projectName, resources]
  );
  const suggestions = useMemo(
    () =>
      variableSuggestions(
        resourcesWithDrafts,
        services,
        domains,
        service.id,
        drafts,
        serviceChanges
      ),
    [domains, drafts, resourcesWithDrafts, service.id, serviceChanges, services]
  );
  const runtimeResolver = resolvedRaw
    ? () => fetchResolvedServiceEnvironment(projectID, service.id)
    : undefined;
  const buildResolver = resolvedRaw
    ? () => fetchResolvedServiceBuildEnvironment(projectID, service.id)
    : undefined;

  return (
    <div className="grid gap-3">
      <div className="flex min-h-12 items-center justify-between gap-4 border border-border bg-muted/15 px-4 py-2.5">
        <div>
          <p className="text-[10px] font-medium">System environment</p>
          <p className="mt-0.5 text-[9px] text-muted-foreground">
            Inspect the defaults and deployment context injected by platformd.
          </p>
        </div>
        <ServiceSystemVariablesDialog />
      </div>
      <VariableSection
        busy={busy}
        environment={service.environment}
        onSave={(environment) => onSave({ environment })}
        resolve={runtimeResolver}
        scope="runtime"
        suggestions={suggestions}
        suggestionsError={suggestionsError}
      />
      {service.source.type === "github" ? (
        <VariableSection
          busy={busy}
          environment={service.buildEnvironment}
          onSave={(buildEnvironment) => onSave({ buildEnvironment })}
          resolve={buildResolver}
          scope="build"
          suggestions={suggestions}
          suggestionsError={suggestionsError}
        />
      ) : null}
    </div>
  );
};
