import type {
  ManagedPostgresInitialCredentials,
  ManagedRedisInitialCredentials,
  ObjectStoreInitialCredentials,
} from "@/api";
import { newID } from "@/id";

const credentialBytes = 32;

const randomBase64URL = () => {
  const bytes = crypto.getRandomValues(new Uint8Array(credentialBytes));
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCodePoint(byte);
  }
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replace(/=+$/u, "");
};

export const createPostgresDraftCredentials =
  (): ManagedPostgresInitialCredentials => {
    const identifier = newID();
    return {
      databaseName: `app_${identifier}`,
      ownerPassword: randomBase64URL(),
      ownerUsername: `owner_${identifier}`,
    };
  };

export const createRedisDraftCredentials =
  (): ManagedRedisInitialCredentials => ({ password: randomBase64URL() });

export const createObjectStoreDraftCredentials =
  (): ObjectStoreInitialCredentials => ({
    accessKey: `ps3_${newID()}`,
    secret: randomBase64URL(),
  });
