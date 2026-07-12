import { expect, test } from "bun:test";

import { fetchMeta } from "@/api";

const invalidMetaFetcher = () =>
  Promise.resolve(Response.json({ status: "healthy", version: 1 }));

const validMetaFetcher = () =>
  Promise.resolve(
    Response.json({
      architecture: "amd64",
      os: "linux",
      status: "ready",
      version: "0.1.0",
    })
  );

test("rejects a malformed control-plane metadata response", async () => {
  await expect(fetchMeta(undefined, invalidMetaFetcher)).rejects.toThrow();
});

test("returns validated control-plane metadata", async () => {
  await expect(fetchMeta(undefined, validMetaFetcher)).resolves.toEqual({
    architecture: "amd64",
    os: "linux",
    status: "ready",
    version: "0.1.0",
  });
});
