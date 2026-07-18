import { expect, test } from "bun:test";

import {
  certificateHostnameSuggestionMatches,
  certificateHostnameSuggestions,
  completeCertificateHostname,
} from "@/certificate-hostname-model";
import type { CertificateHostnameSuggestion } from "@/certificate-hostname-model";

const wildcardSuggestion = {
  dnsName: "*.example.com",
  wildcard: true,
} satisfies CertificateHostnameSuggestion;

const suggestions = certificateHostnameSuggestions([
  {
    createdAt: 1,
    dnsNames: ["*.example.com", "registry.example.com"],
    id: "certificate-1",
  },
  {
    createdAt: 2,
    dnsNames: ["REGISTRY.example.com"],
    id: "certificate-2",
  },
]);

test("deduplicates certificate hostname suggestions", () => {
  expect(suggestions).toEqual([
    wildcardSuggestion,
    { dnsName: "registry.example.com", wildcard: false },
  ]);
});

test("completes one hostname label from a wildcard certificate", () => {
  expect(completeCertificateHostname(wildcardSuggestion, "api")).toBe(
    "api.example.com"
  );
  expect(completeCertificateHostname(wildcardSuggestion, "api.ex")).toBe(
    "api.example.com"
  );
  expect(
    completeCertificateHostname(wildcardSuggestion, "api.example.com")
  ).toBe("api.example.com");
});

test("does not offer an unusable wildcard before a hostname label is typed", () => {
  expect(certificateHostnameSuggestionMatches(wildcardSuggestion, "")).toBe(
    false
  );
  expect(
    certificateHostnameSuggestionMatches(wildcardSuggestion, "api.ex")
  ).toBe(true);
  expect(
    certificateHostnameSuggestionMatches(wildcardSuggestion, "-invalid")
  ).toBe(false);
  expect(completeCertificateHostname(wildcardSuggestion, "")).toBe("");
});
