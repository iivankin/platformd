import type {
  ProjectCanvas,
  Service,
  ServiceDomain,
  ServiceSource,
} from "@/api";
import { newID } from "@/id";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import type { PendingServiceSettings } from "@/service-settings-model";

export const environmentName = /^[A-Za-z_][A-Za-z0-9_]*$/u;

export interface VariableRow {
  id: string;
  name: string;
  value: string;
}

export interface VariableSuggestion {
  expression: string;
  source: string;
  variableName: string;
}

export interface VariableReferenceInsertion {
  cursor: number;
  value: string;
}

const referenceStart = "${{";
const referenceEnd = "}}";
const referenceTokenCharacter = /[A-Za-z0-9_.-]/u;

const boundedCursor = (value: string, cursor?: number) =>
  Math.max(0, Math.min(cursor ?? value.length, value.length));

const replaceVariableRange = (
  value: string,
  expression: string,
  start: number,
  end: number
): VariableReferenceInsertion => ({
  cursor: start + expression.length,
  value: value.slice(0, start) + expression + value.slice(end),
});

const referenceRangeAt = (value: string, start: number, end: number) => {
  let searchFrom = 0;
  while (searchFrom < value.length) {
    const referenceOpen = value.indexOf(referenceStart, searchFrom);
    if (referenceOpen === -1) {
      return;
    }
    const referenceClose = value.indexOf(
      referenceEnd,
      referenceOpen + referenceStart.length
    );
    const rangeEnd =
      referenceClose === -1 ? end : referenceClose + referenceEnd.length;
    const intersects =
      start === end
        ? start >= referenceOpen && start <= rangeEnd
        : start < rangeEnd && end > referenceOpen;
    if (intersects) {
      return { end: rangeEnd, start: referenceOpen };
    }
    if (referenceClose === -1) {
      return;
    }
    searchFrom = rangeEnd;
  }
};

const isSingleWrappedReference = (value: string) => {
  let depth = 0;
  let position = 0;
  while (position < value.length) {
    if (value.startsWith(referenceStart, position)) {
      depth += 1;
      position += referenceStart.length;
      continue;
    }
    if (value.startsWith(referenceEnd, position)) {
      depth -= 1;
      position += referenceEnd.length;
      if (depth === 0 && position < value.length) {
        return false;
      }
      if (depth < 0) {
        return false;
      }
      continue;
    }
    position += 1;
  }
  return depth === 0;
};

export const variableReferenceQuery = (value: string, cursor?: number) => {
  const position = boundedCursor(value, cursor);
  const start = value.lastIndexOf(referenceStart, position);
  const closedBeforeCursor = value.lastIndexOf(referenceEnd, position);
  if (start > closedBeforeCursor) {
    return value.slice(start + referenceStart.length, position).trim();
  }

  let tokenStart = position;
  while (
    tokenStart > 0 &&
    referenceTokenCharacter.test(value[tokenStart - 1] ?? "")
  ) {
    tokenStart -= 1;
  }
  return value.slice(tokenStart, position);
};

export const variableSuggestionMatches = (
  suggestion: VariableSuggestion,
  query: string
) => {
  const normalizedQuery = query.trim().toLocaleLowerCase();
  if (!normalizedQuery) {
    return true;
  }
  return [
    suggestion.expression,
    suggestion.source,
    suggestion.variableName,
  ].some((candidate) =>
    candidate.toLocaleLowerCase().includes(normalizedQuery)
  );
};

export const insertVariableReference = (
  value: string,
  expression: string,
  selectionStart?: number,
  selectionEnd?: number
): VariableReferenceInsertion => {
  const firstPosition = boundedCursor(value, selectionStart);
  const secondPosition = boundedCursor(value, selectionEnd ?? firstPosition);
  const start = Math.min(firstPosition, secondPosition);
  const end = Math.max(firstPosition, secondPosition);

  const contentStart = value.length - value.trimStart().length;
  const contentEnd = value.trimEnd().length;
  const content = value.slice(contentStart, contentEnd);
  if (
    content.startsWith(referenceStart) &&
    content.endsWith(referenceEnd) &&
    isSingleWrappedReference(content)
  ) {
    // A reference-only value is always replaced as one unit. Besides matching
    // normal editor behavior, this prevents accidental `${{${{…}}}}` nesting.
    return replaceVariableRange(value, expression, contentStart, contentEnd);
  }

  const referenceRange = referenceRangeAt(value, start, end);
  if (referenceRange) {
    return replaceVariableRange(
      value,
      expression,
      referenceRange.start,
      referenceRange.end
    );
  }
  if (start !== end) {
    return replaceVariableRange(value, expression, start, end);
  }

  let replaceStart = start;
  while (
    replaceStart > 0 &&
    referenceTokenCharacter.test(value[replaceStart - 1] ?? "")
  ) {
    replaceStart -= 1;
  }
  let replaceEnd = start;
  while (
    replaceEnd < value.length &&
    referenceTokenCharacter.test(value[replaceEnd] ?? "")
  ) {
    replaceEnd += 1;
  }
  return replaceVariableRange(value, expression, replaceStart, replaceEnd);
};

const staticOutputs: Record<
  Exclude<ProjectCanvas["resources"][number]["kind"], "service">,
  string[]
> = {
  error_tracker: [],
  network_gateway: ["HOST", "PORT", "ADDRESS"],
  object_store: [
    "S3_ENDPOINT",
    "S3_REGION",
    "S3_BUCKET",
    "S3_ACCESS_KEY_ID",
    "S3_SECRET_ACCESS_KEY",
  ],
  postgres: [
    "POSTGRES_URL",
    "DATABASE_URL",
    "PGHOST",
    "PGPORT",
    "PGDATABASE",
    "PGUSER",
    "PGPASSWORD",
  ],
  redis: ["REDIS_URL", "REDISHOST", "REDISPORT", "REDISPASSWORD"],
};

const staticServiceOutputs = [
  "PLATFORMD_PROJECT_ID",
  "PLATFORMD_PROJECT_NAME",
  "PLATFORMD_SERVICE_ID",
  "PLATFORMD_SERVICE_NAME",
  "PLATFORMD_PRIVATE_DOMAIN",
  "PLATFORMD_PUBLIC_URLS",
] as const;

export const serviceVariableRows = (
  service: Pick<Service, "environment">
): VariableRow[] =>
  Object.entries(service.environment)
    .map(([name, value]) => ({ id: newID(), name, value }))
    .toSorted((left, right) => left.name.localeCompare(right.name));

const suggestionsForService = (
  resource: ProjectCanvas["resources"][number],
  environment: Record<string, string>,
  resourceDomains: ServiceDomain[],
  source: ServiceSource | undefined,
  currentServiceID: string
): VariableSuggestion[] => {
  const suggestions: VariableSuggestion[] = [];
  if (resource.id !== currentServiceID) {
    for (const output of Object.keys(environment)) {
      suggestions.push({
        expression: `\${{${resource.name}.${output}}}`,
        source: resource.name,
        variableName: output,
      });
    }
  }
  const automaticOutputs = staticServiceOutputs;
  for (const output of automaticOutputs) {
    suggestions.push({
      expression: `\${{${resource.name}.${output}}}`,
      source: resource.name,
      variableName: output,
    });
  }
  const seenDomainOutputs = new Set<string>();
  const duplicateDomainOutputs = new Set<string>();
  for (const domain of resourceDomains) {
    for (const output of [domain.publicOutputName, domain.internalOutputName]) {
      if (seenDomainOutputs.has(output)) {
        duplicateDomainOutputs.add(output);
      }
      seenDomainOutputs.add(output);
    }
  }
  for (const output of seenDomainOutputs) {
    if (!duplicateDomainOutputs.has(output)) {
      suggestions.push({
        expression: `\${{${resource.name}.${output}}}`,
        source: resource.name,
        variableName: output,
      });
    }
  }
  return suggestions;
};

export const variableSuggestions = (
  resources: ProjectCanvas["resources"],
  services: Map<string, Service>,
  domains: Map<string, ServiceDomain[]>,
  currentServiceID: string,
  drafts: PendingResourceCreation[] = [],
  changes: Readonly<Record<string, PendingServiceSettings>> = {}
): VariableSuggestion[] => {
  const draftServices = new Map(
    drafts.flatMap((draft) =>
      draft.kind === "service" ? [[draft.id, draft] as const] : []
    )
  );
  const suggestions: {
    sourceOrder: number;
    suggestion: VariableSuggestion;
  }[] = [];
  for (const resource of resources) {
    if (resource.kind === "service") {
      const service = services.get(resource.id);
      const draftService = draftServices.get(resource.id);
      const change = changes[resource.id];
      const environment =
        draftService?.input.environment ??
        change?.environment ??
        service?.environment ??
        {};
      const source =
        draftService?.settings.configuration.source ??
        change?.draft.configuration.source ??
        service?.source;
      suggestions.push(
        ...suggestionsForService(
          resource,
          environment,
          domains.get(resource.id) ?? [],
          source,
          currentServiceID
        ).map((suggestion) => ({ sourceOrder: 1, suggestion }))
      );
      continue;
    }
    const outputs =
      resource.kind === "network_gateway" && resource.gatewayMode === "export"
        ? []
        : staticOutputs[resource.kind];
    for (const output of outputs) {
      suggestions.push({
        sourceOrder: 0,
        suggestion: {
          expression: `\${{${resource.name}.${output}}}`,
          source: resource.name,
          variableName: output,
        },
      });
    }
  }
  return suggestions
    .toSorted(
      (left, right) =>
        left.suggestion.variableName.localeCompare(
          right.suggestion.variableName
        ) ||
        left.sourceOrder - right.sourceOrder ||
        left.suggestion.source.localeCompare(right.suggestion.source)
    )
    .map(({ suggestion }) => suggestion);
};
