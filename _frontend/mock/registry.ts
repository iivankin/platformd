import type { RegistryRepository } from "../web/api";
import {
  apiSegments,
  booleanField,
  json,
  mockError,
  noContent,
  readObject,
  stringField,
} from "./http";
import type { MockState } from "./state";
import { mockNow, nextMockID } from "./state";

const repositoryByID = (state: MockState, repositoryID: string) =>
  state.registryRepositories.find(
    (repository) => repository.id === repositoryID
  );

const handleRegistrySettings = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, resource, ...rest] = segments;
  if (root !== "registry" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !resource) {
    return json({ hostname: state.registryHostname });
  }
  if (request.method !== "PUT" || resource !== "hostname") {
    return undefined;
  }
  const input = await readObject(request);
  state.registryHostname = stringField(input, "hostname");
  return json({ hostname: state.registryHostname });
};

const handleRepositoryCollection = async (
  request: Request,
  state: MockState,
  segments: string[]
): Promise<Response | undefined> => {
  const [root, resource, ...rest] = segments;
  if (root !== "registry" || resource !== "repositories" || rest.length > 0) {
    return undefined;
  }
  if (request.method === "GET") {
    return json({ repositories: state.registryRepositories });
  }
  if (request.method !== "POST") {
    return undefined;
  }
  const input = await readObject(request);
  const repositoryID = nextMockID(state, "repository");
  const permission =
    stringField(input, "credentialPermission") === "pull"
      ? "pull"
      : "pull_push";
  const repository: RegistryRepository = {
    backupEnabled: false,
    backupRetentionCount: 5,
    blobCount: 0,
    createdAt: mockNow(),
    id: repositoryID,
    manifestCount: 0,
    name: stringField(input, "name", "mock/repository"),
    publicPull: booleanField(input, "publicPull"),
    referencedBlobBytes: 0,
    tagCount: 0,
    totalBlobBytes: 0,
    updatedAt: mockNow(),
  };
  state.registryRepositories = [repository, ...state.registryRepositories];
  state.registryImages[repositoryID] = [];
  const credentialID = nextMockID(state, "registry-credential");
  const username = `robot-${repositoryID}`;
  const secret = "mock-only-registry-secret";
  state.registryCredentials[repositoryID] = [
    {
      createdAt: mockNow(),
      id: credentialID,
      name: stringField(input, "credentialName", "deployer"),
      permission,
      secret,
      secretAvailable: true,
      username,
    },
  ];
  return json({
    ...repository,
    credentialName: stringField(input, "credentialName", "deployer"),
    credentialPermission: permission,
    secret,
    username,
  });
};

const handleRepository = async (
  request: Request,
  state: MockState,
  repositoryID: string,
  rest: string[]
): Promise<Response | undefined> => {
  const [action, ...tail] = rest;
  if (request.method === "DELETE" && !action) {
    const repository = repositoryByID(state, repositoryID);
    if (!repository) {
      return mockError("not_found", "Repository not found", 404);
    }
    const input = await readObject(request);
    if (stringField(input, "expectedName") !== repository.name) {
      return mockError("name_mismatch", "Repository name did not match");
    }
    state.registryRepositories = state.registryRepositories.filter(
      (candidate) => candidate.id !== repositoryID
    );
    state.registryImages[repositoryID] = [];
    state.registryCredentials[repositoryID] = [];
    return noContent();
  }
  if (request.method === "PUT" && action === "public-pull" && tail.length < 1) {
    const repository = repositoryByID(state, repositoryID);
    if (!repository) {
      return mockError("not_found", "Repository not found", 404);
    }
    const input = await readObject(request);
    repository.publicPull = booleanField(input, "publicPull");
    repository.updatedAt = mockNow();
    return json(repository);
  }
  if (request.method !== "POST" || action !== "cleanup" || tail.length > 0) {
    return undefined;
  }
  const input = await readObject(request);
  const dryRun = booleanField(input, "dryRun", true);
  return json({
    blobCount: 1,
    bytes: 12_582_912,
    deleted: !dryRun,
    previewDigests: ["sha256:unreferenced-mock"],
    previewTruncated: false,
  });
};

const handleImages = (
  request: Request,
  state: MockState,
  repositoryID: string,
  rest: string[]
): Response | undefined => {
  const [resource, identifier, ...tail] = rest;
  if (tail.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && resource === "images" && !identifier) {
    return json({
      images: state.registryImages[repositoryID] ?? [],
      nextCursor: "",
    });
  }
  if (request.method === "GET" && resource === "images" && identifier) {
    const image = (state.registryImages[repositoryID] ?? []).find(
      (candidate) => candidate.digest === identifier
    );
    return image ? json(image) : mockError("not_found", "Image not found", 404);
  }
  if (request.method === "DELETE" && resource === "tags" && identifier) {
    state.registryImages[repositoryID] = (
      state.registryImages[repositoryID] ?? []
    ).map((image) => ({
      ...image,
      tags: image.tags.filter((candidate) => candidate !== identifier),
    }));
    return noContent();
  }
  if (request.method !== "DELETE" || resource !== "manifests" || !identifier) {
    return undefined;
  }
  state.registryImages[repositoryID] = (
    state.registryImages[repositoryID] ?? []
  ).filter((image) => image.digest !== identifier);
  return noContent();
};

const handleCredentials = async (
  request: Request,
  state: MockState,
  repositoryID: string,
  rest: string[]
): Promise<Response | undefined> => {
  const [resource, credentialID, ...tail] = rest;
  if (resource !== "credentials" || tail.length > 0) {
    return undefined;
  }
  if (request.method === "GET" && !credentialID) {
    return json({
      credentials: state.registryCredentials[repositoryID] ?? [],
    });
  }
  if (request.method === "DELETE" && credentialID) {
    state.registryCredentials[repositoryID] = (
      state.registryCredentials[repositoryID] ?? []
    ).filter((credential) => credential.id !== credentialID);
    return noContent();
  }
  if (request.method !== "POST" || credentialID) {
    return undefined;
  }
  const input = await readObject(request);
  const permission =
    stringField(input, "permission") === "pull" ? "pull" : "pull_push";
  const credential = {
    createdAt: mockNow(),
    id: nextMockID(state, "registry-credential"),
    name: stringField(input, "name", "mock-credential"),
    permission,
    secret: "mock-only-registry-secret",
    secretAvailable: true,
    username: `robot-${nextMockID(state, "registry-user")}`,
  } as const;
  state.registryCredentials[repositoryID] = [
    ...(state.registryCredentials[repositoryID] ?? []),
    credential,
  ];
  return json(credential);
};

export const handleRegistryAPI = async (
  request: Request,
  state: MockState,
  pathname: string
): Promise<Response | undefined> => {
  const segments = apiSegments(pathname);
  const settingsResponse = await handleRegistrySettings(
    request,
    state,
    segments
  );
  if (settingsResponse) {
    return settingsResponse;
  }
  const collectionResponse = await handleRepositoryCollection(
    request,
    state,
    segments
  );
  if (collectionResponse) {
    return collectionResponse;
  }
  const [root, resource, repositoryID, ...rest] = segments;
  if (root !== "registry" || resource !== "repositories" || !repositoryID) {
    return undefined;
  }
  return (
    (await handleRepository(request, state, repositoryID, rest)) ??
    handleImages(request, state, repositoryID, rest) ??
    (await handleCredentials(request, state, repositoryID, rest))
  );
};
