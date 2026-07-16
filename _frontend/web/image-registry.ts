import type { ImageCredential } from "@/api";

const canonicalHost = (value: string) => value.trim().toLowerCase();

export const imageRegistryHost = (reference: string): string | undefined => {
  const value = reference.trim();
  if (
    !value ||
    value.includes("//") ||
    /\s/u.test(value) ||
    value.startsWith("/") ||
    value.endsWith("/")
  ) {
    return undefined;
  }

  const name = value.split("@", 1)[0] ?? "";
  const slash = name.indexOf("/");
  if (slash === -1) {
    return "docker.io";
  }

  const firstComponent = name.slice(0, slash);
  if (
    firstComponent === "localhost" ||
    firstComponent.includes(".") ||
    firstComponent.includes(":")
  ) {
    return canonicalHost(firstComponent);
  }
  return "docker.io";
};

export const isEmbeddedRegistryReference = (
  reference: string,
  embeddedRegistryHost: string
) => {
  const imageHost = imageRegistryHost(reference);
  return (
    imageHost !== undefined &&
    canonicalHost(embeddedRegistryHost) !== "" &&
    imageHost === canonicalHost(embeddedRegistryHost)
  );
};

export const matchingImageCredentials = (
  credentials: ImageCredential[],
  reference: string
) => {
  const imageHost = imageRegistryHost(reference);
  return imageHost
    ? credentials.filter(
        (credential) => canonicalHost(credential.registryHost) === imageHost
      )
    : [];
};

export const compatibleImageCredentialID = (
  credentialID: string,
  credentials: ImageCredential[],
  reference: string,
  embeddedRegistryHost: string
) => {
  if (
    !credentialID ||
    isEmbeddedRegistryReference(reference, embeddedRegistryHost)
  ) {
    return "";
  }
  return matchingImageCredentials(credentials, reference).some(
    (credential) => credential.id === credentialID
  )
    ? credentialID
    : "";
};
