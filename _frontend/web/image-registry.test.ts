import { describe, expect, test } from "bun:test";

import type { ImageCredential } from "@/api";
import {
  compatibleImageCredentialID,
  imageRegistryHost,
  isEmbeddedRegistryReference,
  matchingImageCredentials,
} from "@/image-registry";

const credentials: ImageCredential[] = [
  {
    createdAt: 1,
    id: "ghcr",
    name: "production",
    registryHost: "ghcr.io",
    username: "robot",
  },
  {
    createdAt: 2,
    id: "docker",
    name: "docker-hub",
    registryHost: "docker.io",
    username: "robot",
  },
];

describe("image registry access", () => {
  test("derives the registry host using Docker reference rules", () => {
    expect(imageRegistryHost("ghcr.io/acme/api:latest")).toBe("ghcr.io");
    expect(imageRegistryHost("localhost:5000/acme/api:latest")).toBe(
      "localhost:5000"
    );
    expect(imageRegistryHost("alpine:3.22")).toBe("docker.io");
    expect(imageRegistryHost("https://ghcr.io/acme/api")).toBeUndefined();
  });

  test("offers only credentials for the exact image host", () => {
    expect(matchingImageCredentials(credentials, "ghcr.io/acme/api")).toEqual(
      credentials.slice(0, 1)
    );
    expect(
      compatibleImageCredentialID(
        "docker",
        credentials,
        "ghcr.io/acme/api",
        "registry.example.com"
      )
    ).toBe("");
  });

  test("does not attach remote credentials to the built-in registry", () => {
    expect(
      isEmbeddedRegistryReference(
        "registry.example.com/acme/api",
        "registry.example.com"
      )
    ).toBe(true);
    expect(
      compatibleImageCredentialID(
        "ghcr",
        credentials,
        "registry.example.com/acme/api",
        "registry.example.com"
      )
    ).toBe("");
  });
});
