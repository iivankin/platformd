import type { ManagedPostgres, ManagedRedis, ObjectStore } from "@/api";

export const postgresConnectionURL = (resource: ManagedPostgres) =>
  `postgres://${encodeURIComponent(resource.ownerUsername)}:${encodeURIComponent(resource.ownerPassword)}@${resource.hostname}:${resource.port}/${encodeURIComponent(resource.databaseName)}`;

export const redisConnectionURL = (resource: ManagedRedis) =>
  `redis://:${encodeURIComponent(resource.password)}@${resource.hostname}:${resource.port}/0`;

export const objectStoreEndpoint = (resource: ObjectStore) =>
  resource.publicHostname
    ? `https://${resource.publicHostname}`
    : `http://${resource.internalHostname}:9000`;

export const objectStoreConfiguration = (resource: ObjectStore) =>
  JSON.stringify(
    {
      accessKeyId: resource.accessKey,
      bucket: resource.bucketName,
      endpoint: objectStoreEndpoint(resource),
      region: resource.region,
      secretAccessKey: resource.secret,
    },
    null,
    2
  );
