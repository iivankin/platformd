import { expect, test } from "bun:test";

import type { ProjectCanvas, Service, ServiceDomain } from "@/api";
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
  createdAt: 1,
  enabled: true,
  environment,
  id,
  imageReference: "docker.io/library/alpine:latest",
  name,
  projectId: "project",
  secretReferences: [],
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
    },
    {
      enabled: true,
      id: "api",
      internalHostname: "api.shop.internal",
      kind: "service",
      name: "api",
      status: "running",
    },
    {
      enabled: true,
      id: "postgres",
      internalHostname: "main.shop.internal",
      kind: "postgres",
      name: "main",
      status: "running",
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
