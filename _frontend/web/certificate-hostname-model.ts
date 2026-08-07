import { getDomain } from "tldts";

import type { InstallationSettings } from "@/api";

export interface CertificateHostnameSuggestion {
  dnsName: string;
  wildcard: boolean;
}

const dnsLabelPattern = /^(?!-)[a-z\d-]{1,63}(?<!-)$/u;

const normalizeInput = (value: string) => value.trim().toLowerCase();

const isRegistrableApex = (hostname: string) => {
  if (!hostname.includes(".")) {
    return false;
  }
  return getDomain(hostname) === hostname;
};

export const certificateHostnameSuggestions = (
  certificates: InstallationSettings["certificates"]
): CertificateHostnameSuggestion[] => {
  const names = new Set(
    certificates.flatMap((certificate) =>
      certificate.dnsNames.map((name) => normalizeInput(name))
    )
  );
  return [...names]
    .filter(Boolean)
    .map((dnsName) => ({
      dnsName,
      wildcard: dnsName.startsWith("*."),
    }))
    .toSorted((left, right) => left.dnsName.localeCompare(right.dnsName));
};

// Apex preview roots come from wildcard Origin names (*.example.com → example.com)
// so a single-label preview child is covered by the same certificate.
export const certificateApexDomainSuggestions = (
  certificates: InstallationSettings["certificates"]
): CertificateHostnameSuggestion[] => {
  const apexes = new Set<string>();
  for (const certificate of certificates) {
    for (const name of certificate.dnsNames) {
      const dnsName = normalizeInput(name);
      if (!dnsName.startsWith("*.")) {
        continue;
      }
      const apex = dnsName.slice(2);
      // Match backend NormalizeApex / IsApex: only eTLD+1 roots are usable.
      if (isRegistrableApex(apex)) {
        apexes.add(apex);
      }
    }
  }
  return [...apexes]
    .map((dnsName) => ({ dnsName, wildcard: false }))
    .toSorted((left, right) => left.dnsName.localeCompare(right.dnsName));
};

export const completeCertificateHostname = (
  suggestion: CertificateHostnameSuggestion,
  input: string
) => {
  if (!suggestion.wildcard) {
    return suggestion.dnsName;
  }
  const query = normalizeInput(input);
  const suffix = suggestion.dnsName.slice(2);
  const coveredSuffix = `.${suffix}`;
  if (query.endsWith(coveredSuffix)) {
    const prefix = query.slice(0, -coveredSuffix.length);
    return dnsLabelPattern.test(prefix) ? query : input;
  }
  const prefix = query.split(".", 1)[0] ?? "";
  return dnsLabelPattern.test(prefix) ? `${prefix}.${suffix}` : input;
};

export const certificateHostnameSuggestionMatches = (
  suggestion: CertificateHostnameSuggestion,
  input: string
) => {
  const query = normalizeInput(input);
  if (!suggestion.wildcard) {
    return query === "" || suggestion.dnsName.includes(query);
  }
  if (query === "") {
    return false;
  }
  const completed = completeCertificateHostname(suggestion, query);
  const suffix = suggestion.dnsName.slice(1);
  const existingPrefix = query.endsWith(suffix)
    ? query.slice(0, -suffix.length)
    : "";
  const alreadyCovered = dnsLabelPattern.test(existingPrefix);
  return (
    (normalizeInput(completed) !== query || alreadyCovered) &&
    normalizeInput(completed).includes(query)
  );
};
