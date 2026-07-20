import type {
  ManagedPostgresInitialCredentials,
  ManagedRedisInitialCredentials,
  ObjectStoreInitialCredentials,
} from "@/api";

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

const randomCompactUUID = () => crypto.randomUUID().replaceAll("-", "");

export const createPostgresDraftCredentials =
  (): ManagedPostgresInitialCredentials => {
    const identifier = randomCompactUUID().slice(0, 24);
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
    accessKey: `ps3_${randomCompactUUID()}`,
    secret: randomBase64URL(),
  });
