import { expect, test } from "bun:test";

import {
  portForwardActionExample,
  projectNameFromInternalHostname,
  uploadImageActionExample,
} from "@/github-action-example-dialog";

test("derives project name from internal hostnames", () => {
  expect(projectNameFromInternalHostname("api.storefront.internal")).toBe(
    "storefront"
  );
  expect(projectNameFromInternalHostname("main.shop.internal")).toBe("shop");
});

test("builds an upload example with url, project, and resource", () => {
  const example = uploadImageActionExample({
    origin: "https://admin.example.com",
    projectName: "storefront",
    serviceName: "api",
  });
  expect(example).toContain("url: https://admin.example.com");
  expect(example).toContain("project: storefront");
  expect(example).toContain("resource: api");
  expect(example).toContain("iivankin/platformd/actions/upload-image@v1");
  expect(example).not.toContain("endpoint:");
  expect(example).not.toContain("secrets.PLATFORMD");
});

test("builds a Postgres migration port-forward example", () => {
  const example = portForwardActionExample({
    kind: "postgres",
    localPort: 15_432,
    origin: "https://admin.example.com",
    port: 5432,
    projectName: "storefront",
    resourceName: "main",
  });
  expect(example).toContain("url: https://admin.example.com");
  expect(example).toContain("project: storefront");
  expect(example).toContain("resource: main");
  expect(example).toContain(`connection-url: \${{ secrets.POSTGRES_URL }}`);
  expect(example).toContain("bun run migrate");
  expect(example).toContain("# Open a temporary tunnel to managed Postgres");
  expect(example).not.toContain("secrets.PLATFORMD");
});

test("builds a service errors port-forward example", () => {
  const example = portForwardActionExample({
    kind: "errors",
    origin: "https://admin.example.com",
    port: 9001,
    projectName: "storefront",
    resourceName: "api",
    sentryProject: "service-id",
  });
  expect(example).toContain("endpoint: errors");
  expect(example).toContain(
    `SENTRY_URL: \${{ steps.sentry.outputs.sentry-url }}`
  );
  expect(example).toContain("SENTRY_PROJECT: service-id");
  expect(example).toContain("SENTRY_AUTH_TOKEN: internal");
  expect(example).not.toContain("secrets.SENTRY_AUTH_TOKEN");
  expect(example).not.toContain("port: 9001");
});
