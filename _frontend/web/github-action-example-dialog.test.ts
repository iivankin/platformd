import { expect, test } from "bun:test";

import {
  portForwardActionExample,
  projectNameFromInternalHostname,
  tokenizeYamlLine,
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

test("tokenizes one YAML key per line and leaves URL colons alone", () => {
  expect(tokenizeYamlLine("  # tunnel postgres")).toEqual([
    { kind: "punctuation", text: "  " },
    { kind: "comment", text: "# tunnel postgres" },
  ]);
  expect(tokenizeYamlLine("  project: storefront")).toEqual([
    { kind: "punctuation", text: "  " },
    { kind: "key", text: "project" },
    { kind: "punctuation", text: ":" },
    { kind: "plain", text: " storefront" },
  ]);
  expect(tokenizeYamlLine("          url: http://127.0.0.1:3100")).toEqual([
    { kind: "punctuation", text: "          " },
    { kind: "key", text: "url" },
    { kind: "punctuation", text: ":" },
    { kind: "plain", text: " http://127.0.0.1:3100" },
  ]);
  expect(tokenizeYamlLine("      - uses: actions/checkout@v4")).toEqual([
    { kind: "punctuation", text: "      - " },
    { kind: "key", text: "uses" },
    { kind: "punctuation", text: ":" },
    { kind: "plain", text: " actions/checkout@v4" },
  ]);
  const expression = `\${{ runner.temp }}`;
  expect(
    tokenizeYamlLine(`          archive: ${expression}/image.oci`)
  ).toEqual([
    { kind: "punctuation", text: "          " },
    { kind: "key", text: "archive" },
    { kind: "punctuation", text: ":" },
    { kind: "plain", text: " " },
    { kind: "expression", text: expression },
    { kind: "plain", text: "/image.oci" },
  ]);
});
