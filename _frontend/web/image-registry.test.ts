import { describe, expect, test } from "bun:test";

import { imageRegistryHost } from "@/image-registry";

describe("image registry access", () => {
  test("derives the registry host using Docker reference rules", () => {
    expect(imageRegistryHost("ghcr.io/acme/api:latest")).toBe("ghcr.io");
    expect(imageRegistryHost("localhost:5000/acme/api:latest")).toBe(
      "localhost:5000"
    );
    expect(imageRegistryHost("alpine:3.22")).toBe("docker.io");
    expect(imageRegistryHost("https://ghcr.io/acme/api")).toBeUndefined();
  });
});
