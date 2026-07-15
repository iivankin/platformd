import { Copy, Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { fetchProjectCanvas } from "@/api";
import type { ProjectCanvas, Service } from "@/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

const environmentName = /^[A-Za-z_][A-Za-z0-9_]*$/u;

const resourceOutputs: Record<
  ProjectCanvas["resources"][number]["kind"],
  string[]
> = {
  object_store: [
    "S3_ENDPOINT",
    "S3_REGION",
    "S3_BUCKET",
    "S3_ACCESS_KEY_ID",
    "S3_SECRET_ACCESS_KEY",
  ],
  postgres: [
    "PGHOST",
    "PGPORT",
    "PGDATABASE",
    "PGUSER",
    "PGPASSWORD",
    "DATABASE_URL",
  ],
  redis: ["REDISHOST", "REDISPORT", "REDISPASSWORD", "REDIS_URL"],
  service: ["HOST", "PORT", "URL"],
};

interface LiteralVariable {
  id: string;
  name: string;
  value: string;
}

const literalRows = (environment: Record<string, string>) =>
  Object.entries(environment)
    .toSorted(([left], [right]) => left.localeCompare(right))
    .map(([name, value]) => ({ id: crypto.randomUUID(), name, value }));

export const ServiceVariables = ({
  busy,
  onSave,
  projectID,
  service,
}: {
  busy: boolean;
  onSave: (
    environment: Record<string, string>,
    references: Service["resourceReferences"]
  ) => Promise<boolean>;
  projectID: string;
  service: Service;
}) => {
  const [literals, setLiterals] = useState<LiteralVariable[]>(() =>
    literalRows(service.environment)
  );
  const [references, setReferences] = useState(service.resourceReferences);
  const [resources, setResources] = useState<ProjectCanvas["resources"]>([]);
  const [newName, setNewName] = useState("");
  const [selectedOutput, setSelectedOutput] = useState("");
  const [error, setError] = useState<string>();

  useEffect(() => {
    const controller = new AbortController();
    const loadResources = async () => {
      try {
        const canvas = await fetchProjectCanvas(projectID, controller.signal);
        setResources(
          canvas.resources.filter((resource) => resource.id !== service.id)
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
            : "Unable to load resource variables"
        );
      }
    };
    void loadResources();
    return () => controller.abort();
  }, [projectID, service.id]);

  const outputs = useMemo(
    () =>
      resources.flatMap((resource) =>
        resourceOutputs[resource.kind].map((outputName) => ({
          label: `${resource.name}.${outputName}`,
          outputName,
          resourceId: resource.id,
          resourceKind: resource.kind,
        }))
      ),
    [resources]
  );

  const addLiteral = () => {
    setLiterals((current) => [
      ...current,
      { id: crypto.randomUUID(), name: "", value: "" },
    ]);
  };

  const addReference = () => {
    const output = outputs.find(
      (candidate) =>
        `${candidate.resourceKind}:${candidate.resourceId}:${candidate.outputName}` ===
        selectedOutput
    );
    if (!(environmentName.test(newName) && output)) {
      setError("Choose a resource output and enter a valid variable name.");
      return;
    }
    setReferences((current) => [
      ...current,
      { environmentName: newName, ...output },
    ]);
    setNewName("");
    setSelectedOutput("");
    setError(undefined);
  };

  const save = async () => {
    const environment: Record<string, string> = {};
    const names = new Set<string>();
    for (const row of literals) {
      if (!environmentName.test(row.name)) {
        setError(`Invalid environment name: ${row.name || "(empty)"}`);
        return;
      }
      if (names.has(row.name)) {
        setError(`Duplicate environment name: ${row.name}`);
        return;
      }
      names.add(row.name);
      environment[row.name] = row.value;
    }
    for (const reference of references) {
      if (names.has(reference.environmentName)) {
        setError(`Duplicate environment name: ${reference.environmentName}`);
        return;
      }
      names.add(reference.environmentName);
    }
    if (await onSave(environment, references)) {
      setError(undefined);
    }
  };

  return (
    <div>
      <section className="flex min-h-14 items-center justify-between gap-4 border-b border-border px-5 py-3">
        <div>
          <h3 className="text-[10px] font-medium">Service variables</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Literal values stay here. References resolve when a deployment
            starts.
          </p>
        </div>
        <Button onClick={addLiteral} size="sm" variant="outline">
          <Plus /> Variable
        </Button>
      </section>

      <div className="border-b border-border">
        {literals.map((row) => (
          <div
            className="grid grid-cols-[minmax(10rem,0.8fr)_minmax(16rem,1.4fr)_2.5rem] border-b border-border last:border-b-0"
            key={row.id}
          >
            <Input
              aria-label="Variable name"
              className="h-11 border-0 border-r px-5 font-mono focus-visible:ring-0"
              onChange={(event) =>
                setLiterals((current) =>
                  current.map((candidate) =>
                    candidate.id === row.id
                      ? { ...candidate, name: event.target.value }
                      : candidate
                  )
                )
              }
              placeholder="VARIABLE_NAME"
              value={row.name}
            />
            <Input
              aria-label={`${row.name || "Variable"} value`}
              className="h-11 border-0 border-r px-5 font-mono focus-visible:ring-0"
              onChange={(event) =>
                setLiterals((current) =>
                  current.map((candidate) =>
                    candidate.id === row.id
                      ? { ...candidate, value: event.target.value }
                      : candidate
                  )
                )
              }
              placeholder="value"
              value={row.value}
            />
            <Button
              aria-label={`Remove ${row.name || "variable"}`}
              className="h-11 w-full"
              onClick={() =>
                setLiterals((current) =>
                  current.filter((candidate) => candidate.id !== row.id)
                )
              }
              size="icon"
              variant="ghost"
            >
              <Trash2 />
            </Button>
          </div>
        ))}
      </div>

      <section className="border-b border-border">
        <div className="border-b border-border px-5 py-3">
          <h3 className="text-[10px] font-medium">References</h3>
          <p className="mt-1 text-[9px] text-muted-foreground">
            Values follow the selected resource without copying credentials into
            service state.
          </p>
        </div>
        {references.map((reference) => {
          const resource = resources.find(
            (candidate) => candidate.id === reference.resourceId
          );
          return (
            <div
              className="grid min-h-11 grid-cols-[minmax(10rem,0.8fr)_minmax(16rem,1.4fr)_2.5rem] items-center border-b border-border last:border-b-0"
              key={reference.environmentName}
            >
              <code className="px-5 text-[10px]">
                {reference.environmentName}
              </code>
              <div className="flex min-w-0 items-center gap-2 border-l border-border px-5 text-[10px]">
                <span className="truncate text-muted-foreground">
                  {resource?.name ?? reference.resourceId}
                </span>
                <span aria-hidden="true">→</span>
                <code>{reference.outputName}</code>
                <Button
                  aria-label={`Copy ${reference.environmentName} reference`}
                  className="ml-auto"
                  onClick={() =>
                    void navigator.clipboard.writeText(
                      `\${{${resource?.name ?? reference.resourceId}.${reference.outputName}}}`
                    )
                  }
                  size="icon"
                  variant="ghost"
                >
                  <Copy />
                </Button>
              </div>
              <Button
                aria-label={`Remove ${reference.environmentName} reference`}
                className="h-11 w-full border-l border-border"
                onClick={() =>
                  setReferences((current) =>
                    current.filter(
                      (candidate) =>
                        candidate.environmentName !== reference.environmentName
                    )
                  )
                }
                size="icon"
                variant="ghost"
              >
                <Trash2 />
              </Button>
            </div>
          );
        })}
        <div className="grid grid-cols-[minmax(10rem,0.8fr)_minmax(16rem,1.4fr)_auto] gap-2 px-5 py-3">
          <Input
            aria-label="Referenced variable name"
            onChange={(event) => setNewName(event.target.value)}
            placeholder="DATABASE_URL"
            value={newName}
          />
          <select
            aria-label="Resource output"
            className="h-8 border border-input bg-background px-2 text-[10px] outline-none focus-visible:border-ring"
            onChange={(event) => setSelectedOutput(event.target.value)}
            value={selectedOutput}
          >
            <option value="">Select resource output…</option>
            {outputs.map((output) => {
              const value = `${output.resourceKind}:${output.resourceId}:${output.outputName}`;
              return (
                <option key={value} value={value}>
                  {output.label}
                </option>
              );
            })}
          </select>
          <Button onClick={addReference} size="sm">
            <Plus /> Reference
          </Button>
        </div>
      </section>

      <div className="flex items-center justify-end gap-3 border-b border-border px-5 py-3">
        {error ? (
          <p className="mr-auto text-[10px] text-destructive">{error}</p>
        ) : null}
        <Button disabled={busy} onClick={() => void save()}>
          {busy ? "Saving…" : "Save variables"}
        </Button>
      </div>
    </div>
  );
};
