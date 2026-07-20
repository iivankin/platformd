import { expect, test } from "bun:test";

import {
  createObjectStoreDraftCredentials,
  createPostgresDraftCredentials,
  createRedisDraftCredentials,
} from "@/resource-draft-credentials";

const generatedSecretPattern = /^[\w-]{43}$/u;

test("generates credentials that satisfy managed resource contracts", () => {
  const postgres = createPostgresDraftCredentials();
  const redis = createRedisDraftCredentials();
  const objectStore = createObjectStoreDraftCredentials();

  expect(postgres.databaseName).toMatch(/^app_[a-f\d]{24}$/u);
  expect(postgres.ownerUsername).toMatch(/^owner_[a-f\d]{24}$/u);
  expect(postgres.ownerPassword).toMatch(generatedSecretPattern);
  expect(redis.password).toMatch(generatedSecretPattern);
  expect(objectStore.accessKey).toMatch(/^ps3_[a-f\d]{32}$/u);
  expect(objectStore.secret).toMatch(generatedSecretPattern);
});
