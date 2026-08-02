import { expect, test } from "bun:test";

import type { ProjectCanvas, Service, ServiceDomain } from "@/api";
import {
  emptyPendingBackupPolicy,
  emptyPendingServiceCreationSettings,
  pendingCanvasResource,
} from "@/pending-resource-creation";
import type { PendingResourceCreation } from "@/pending-resource-creation";
import {
  createPendingServiceSettings,
  createServiceSettingsDraft,
} from "@/service-settings-model";
import {
  insertVariableReference,
  variableReferenceQuery,
  variableSuggestionMatches,
  variableSuggestions,
} from "@/service-variable-model";

const reference = (resource: string, output: string) =>
  `\${{${resource}.${output}}}`;

const service = (
  id: string,
  name: string,
  environment: Record<string, string>
): Service => ({
  buildEnvironment: {},
  createdAt: 1,
  enabled: true,
  environment,
  id,
  name,
  projectId: "project",
  secretReferences: [],
  source: {
    autoUpdate: true,
    image: { reference: "docker.io/library/alpine:latest" },
    type: "public_image",
  },
  updatedAt: 1,
  volumeMounts: [],
});

test("variable suggestions fill names and string expressions from project resources", () => {
  const resources: ProjectCanvas["resources"] = [
    {
      enabled: true,
      id: "current",
      internalHostname: "web.shop.internal",
      kind: "service",
      name: "web",
      status: "running",
      volumes: [],
    },
    {
      enabled: true,
      id: "api",
      internalHostname: "api.shop.internal",
      kind: "service",
      name: "api",
      status: "running",
      volumes: [],
    },
    {
      enabled: true,
      id: "postgres",
      internalHostname: "main.shop.internal",
      kind: "postgres",
      name: "main",
      status: "running",
      volumes: [],
    },
  ];
  const services = new Map([
    ["current", service("current", "web", { OWN_VALUE: "hidden" })],
    [
      "api",
      service("api", "api", {
        CUSTOM_ENDPOINT: "/v1",
        PAGE_TOKEN: "secret",
      }),
    ],
  ]);
  const currentDomain: ServiceDomain = {
    createdAt: 1,
    hostname: "shop.example.com",
    internalOutputName: "SHOP_URL_INTERNAL",
    publicOutputName: "SHOP_URL",
    serviceId: "current",
    targetPort: 3000,
  };
  const domains = new Map([["current", [currentDomain]]]);

  const suggestions = variableSuggestions(
    resources,
    services,
    domains,
    "current"
  );
  expect(suggestions).toContainEqual({
    expression: reference("main", "POSTGRES_URL"),
    source: "main",
    variableName: "POSTGRES_URL",
  });
  expect(suggestions).toContainEqual({
    expression: reference("api", "CUSTOM_ENDPOINT"),
    source: "api",
    variableName: "CUSTOM_ENDPOINT",
  });
  expect(suggestions).toContainEqual({
    expression: reference("api", "PAGE_TOKEN"),
    source: "api",
    variableName: "PAGE_TOKEN",
  });
  expect(suggestions).toContainEqual({
    expression: reference("web", "SHOP_URL_INTERNAL"),
    source: "web",
    variableName: "SHOP_URL_INTERNAL",
  });
  expect(
    suggestions.some(({ variableName }) => variableName === "OWN_VALUE")
  ).toBe(false);
});

test("variable suggestions include variables exported by service drafts", () => {
  const resources: ProjectCanvas["resources"] = [
    {
      enabled: true,
      id: "current",
      internalHostname: "web.shop.internal",
      kind: "service",
      name: "web",
      status: "running",
      volumes: [],
    },
    {
      enabled: true,
      id: "draft:api",
      internalHostname: "api.shop.internal",
      kind: "service",
      name: "api",
      status: "pending",
      volumes: [],
    },
  ];
  const input = {
    buildEnvironment: {},
    environment: {
      CUSTOM_ENDPOINT: "/v1",
      PAGE_TOKEN: "secret",
    },
    name: "api",
    source: {
      autoUpdate: true,
      image: { reference: "docker.io/library/alpine:latest" },
      type: "public_image",
    },
  } satisfies Extract<PendingResourceCreation, { kind: "service" }>["input"];
  const draft: PendingResourceCreation = {
    id: "draft:api",
    input,
    kind: "service",
    settings: emptyPendingServiceCreationSettings(input),
  };

  expect(
    variableSuggestions(resources, new Map(), new Map(), "current", [draft])
  ).toEqual([
    {
      expression: reference("api", "CUSTOM_ENDPOINT"),
      source: "api",
      variableName: "CUSTOM_ENDPOINT",
    },
    {
      expression: reference("api", "PAGE_TOKEN"),
      source: "api",
      variableName: "PAGE_TOKEN",
    },
  ]);
});

test("variable suggestions include generated outputs from managed drafts", () => {
  const redis: PendingResourceCreation = {
    backupPolicy: emptyPendingBackupPolicy(),
    id: "draft:cache",
    input: {
      credentials: { password: "password" },
      imageTag: "8.2",
      name: "cache",
    },
    kind: "redis",
  };
  const postgres: PendingResourceCreation = {
    backupPolicy: emptyPendingBackupPolicy(),
    id: "draft:database",
    input: {
      credentials: {
        databaseName: "app",
        ownerPassword: "password",
        ownerUsername: "owner",
      },
      imageTag: "18.3",
      name: "database",
    },
    kind: "postgres",
  };

  const suggestions = variableSuggestions(
    [
      pendingCanvasResource(redis, "shop"),
      pendingCanvasResource(postgres, "shop"),
    ],
    new Map(),
    new Map(),
    "current",
    [redis, postgres]
  );

  expect(
    new Set(
      suggestions
        .filter((suggestion) => suggestion.source === "cache")
        .map((suggestion) => suggestion.variableName)
    )
  ).toEqual(new Set(["REDIS_URL", "REDISHOST", "REDISPORT", "REDISPASSWORD"]));
  expect(
    new Set(
      suggestions
        .filter((suggestion) => suggestion.source === "database")
        .map((suggestion) => suggestion.variableName)
    )
  ).toEqual(
    new Set([
      "POSTGRES_URL",
      "DATABASE_URL",
      "PGHOST",
      "PGPORT",
      "PGDATABASE",
      "PGUSER",
      "PGPASSWORD",
    ])
  );
});

test("managed resource references sort before service variables with the same name", () => {
  const resources: ProjectCanvas["resources"] = [
    {
      enabled: true,
      id: "current",
      internalHostname: "web.shop.internal",
      kind: "service",
      name: "web",
      status: "running",
      volumes: [],
    },
    {
      enabled: true,
      id: "api",
      internalHostname: "api.shop.internal",
      kind: "service",
      name: "api",
      status: "running",
      volumes: [],
    },
    {
      enabled: true,
      id: "redis",
      internalHostname: "redis.shop.internal",
      kind: "redis",
      name: "redis",
      status: "running",
      volumes: [],
    },
  ];

  const suggestions = variableSuggestions(
    resources,
    new Map([
      ["current", service("current", "web", {})],
      ["api", service("api", "api", { REDIS_URL: "redis://example" })],
    ]),
    new Map(),
    "current"
  );

  expect(
    suggestions
      .filter(({ variableName }) => variableName === "REDIS_URL")
      .map(({ expression }) => expression)
  ).toEqual([reference("redis", "REDIS_URL"), reference("api", "REDIS_URL")]);
});

test("variable suggestions include variables staged on existing services", () => {
  const current = service("current", "web", {});
  const api = service("api", "api", { SAVED_OUTPUT: "old" });
  const change = createPendingServiceSettings({
    domains: [],
    draft: createServiceSettingsDraft(api, [], [], []),
    environment: { STAGED_OUTPUT: "new" },
    listeners: [],
    service: api,
    volumes: [],
  });
  if (!change) {
    throw new Error("variable change was not staged");
  }
  const resources: ProjectCanvas["resources"] = [
    {
      enabled: true,
      id: current.id,
      internalHostname: "web.shop.internal",
      kind: "service",
      name: current.name,
      status: "running",
      volumes: [],
    },
    {
      enabled: true,
      id: api.id,
      internalHostname: "api.shop.internal",
      kind: "service",
      name: api.name,
      status: "running",
      volumes: [],
    },
  ];

  const suggestions = variableSuggestions(
    resources,
    new Map([
      [current.id, current],
      [api.id, api],
    ]),
    new Map(),
    current.id,
    [],
    { [api.id]: change }
  );

  expect(suggestions).toContainEqual({
    expression: reference("api", "STAGED_OUTPUT"),
    source: "api",
    variableName: "STAGED_OUTPUT",
  });
  expect(
    suggestions.some(({ variableName }) => variableName === "SAVED_OUTPUT")
  ).toBe(false);
});

test("value suggestions match the reference fragment at the cursor", () => {
  const suggestion = {
    expression: reference("api", "PAGE_TOKEN"),
    source: "api",
    variableName: "PAGE_TOKEN",
  };

  expect(variableReferenceQuery("https://${{api.PAGE", 24)).toBe("api.PAGE");
  expect(variableSuggestionMatches(suggestion, "api.PAGE")).toBe(true);
  expect(variableSuggestionMatches(suggestion, "main.POSTGRES")).toBe(false);
});

test("selecting a value suggestion replaces the current token only", () => {
  const expression = reference("api", "PAGE_TOKEN");

  expect(insertVariableReference("https://api", expression)).toEqual({
    cursor: `https://${expression}`.length,
    value: `https://${expression}`,
  });
  expect(
    insertVariableReference("prefix ${{api.PA suffix", expression, 16)
  ).toEqual({
    cursor: `prefix ${expression}`.length,
    value: `prefix ${expression} suffix`,
  });
});

test("selecting a suggestion replaces an existing reference without nesting", () => {
  const pgHost = reference("main", "PGHOST");
  const pgPort = reference("main", "PGPORT");
  const accidentallyNested = `\${{${pgHost}}}`;
  const adjacentReferences = `${pgHost}:${pgPort}`;

  expect(insertVariableReference(pgHost, pgPort, pgHost.length)).toEqual({
    cursor: pgPort.length,
    value: pgPort,
  });
  expect(
    insertVariableReference(
      accidentallyNested,
      pgHost,
      accidentallyNested.length
    )
  ).toEqual({
    cursor: pgHost.length,
    value: pgHost,
  });
  expect(
    insertVariableReference(
      adjacentReferences,
      pgHost,
      adjacentReferences.length
    )
  ).toEqual({
    cursor: adjacentReferences.length,
    value: `${pgHost}:${pgHost}`,
  });
});
