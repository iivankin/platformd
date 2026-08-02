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

  expect(postgres.databaseName).toMatch(/^app_[a-z][a-z\d]{23}$/u);
  expect(postgres.ownerUsername).toMatch(/^owner_[a-z][a-z\d]{23}$/u);
  expect(postgres.ownerPassword).toMatch(generatedSecretPattern);
  expect(redis.password).toMatch(generatedSecretPattern);
  expect(objectStore.accessKey).toMatch(/^ps3_[a-z][a-z\d]{23}$/u);
  expect(objectStore.secret).toMatch(generatedSecretPattern);
});
