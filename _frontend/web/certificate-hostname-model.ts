import type { InstallationSettings } from "@/api";

export interface CertificateHostnameSuggestion {
  dnsName: string;
  wildcard: boolean;
}

const dnsLabelPattern = /^(?!-)[a-z\d-]{1,63}(?<!-)$/u;

const normalizeInput = (value: string) => value.trim().toLowerCase();

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
