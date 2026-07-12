import { expect, test } from "bun:test";

import {
  createProject,
  createImageCredential,
  createService,
  fetchService,
  fetchServiceDeployments,
  fetchImageCredentials,
  fetchIdentity,
  fetchMeta,
  fetchProjectCanvas,
  fetchProjects,
  redeployService,
  rollbackService,
  updateService,
} from "@/api";

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

test("returns the validated Cloudflare Access identity", async () => {
  await expect(
    fetchIdentity(undefined, () =>
      Promise.resolve(
        Response.json({ email: "admin@example.com", subject: "access-user" })
      )
    )
  ).resolves.toEqual({ email: "admin@example.com", subject: "access-user" });
});

const project = {
  createdAt: 1,
  id: "project-id",
  name: "shop",
  objectStoreCount: 0,
  postgresCount: 0,
  redisCount: 0,
  serviceCount: 0,
  updatedAt: 1,
};

test("validates project list responses", async () => {
  await expect(
    fetchProjects(undefined, () => Promise.resolve(Response.json([project])))
  ).resolves.toEqual([project]);
  await expect(
    fetchProjects(undefined, () =>
      Promise.resolve(Response.json([{ ...project, serviceCount: -1 }]))
    )
  ).rejects.toThrow();
});

test("creates a project with the exact JSON contract", async () => {
  let requestInit: RequestInit | undefined;
  const created = await createProject("shop", (_input, init) => {
    requestInit = init;
    return Promise.resolve(Response.json(project, { status: 201 }));
  });
  expect(created).toEqual(project);
  expect(requestInit?.method).toBe("POST");
  expect(requestInit?.body).toBe('{"name":"shop"}');
});

test("surfaces structured API errors", async () => {
  await expect(
    createProject("shop", () =>
      Promise.resolve(
        Response.json(
          {
            error: { code: "project_name_conflict", message: "Already exists" },
          },
          { status: 409 }
        )
      )
    )
  ).rejects.toThrow("Already exists");
});

test("validates the project canvas and encodes its project ID", async () => {
  let requestURL = "";
  const canvas = await fetchProjectCanvas(
    "project/with slash",
    undefined,
    (input) => {
      requestURL = input.toString();
      return Promise.resolve(
        Response.json({
          connections: [
            {
              environmentNames: ["DATABASE_URL"],
              sourceId: "api",
              targetId: "database",
            },
          ],
          project,
          resources: [
            {
              enabled: true,
              id: "api",
              imageReference: "example/api:latest",
              internalHostname: "api.shop.internal",
              kind: "service",
              name: "api",
              status: "running",
            },
          ],
        })
      );
    }
  );
  expect(requestURL).toBe("/api/v1/projects/project%2Fwith%20slash/canvas");
  expect(canvas.connections[0]?.environmentNames).toEqual(["DATABASE_URL"]);
});

test("creates remote image credentials without changing the JSON fields", async () => {
  let requestInit: RequestInit | undefined;
  const credential = await createImageCredential(
    "project",
    {
      name: "production",
      password: "secret",
      registryHost: "registry.example.com",
      username: "robot",
    },
    (_input, init) => {
      requestInit = init;
      return Promise.resolve(
        Response.json(
          {
            createdAt: 1,
            id: "credential",
            name: "production",
            registryHost: "registry.example.com",
            username: "robot",
          },
          { status: 201 }
        )
      );
    }
  );
  expect(credential.id).toBe("credential");
  expect(requestInit?.body).toBe(
    '{"name":"production","password":"secret","registryHost":"registry.example.com","username":"robot"}'
  );
});

test("lists credentials and creates a service", async () => {
  const credential = {
    createdAt: 1,
    id: "credential",
    name: "production",
    registryHost: "registry.example.com",
    username: "robot",
  };
  await expect(
    fetchImageCredentials("project", undefined, () =>
      Promise.resolve(Response.json([credential]))
    )
  ).resolves.toEqual([credential]);
  let requestInit: RequestInit | undefined;
  const service = await createService(
    "project",
    {
      environment: { APP_ENV: "production" },
      imageCredentialId: "credential",
      imageReference: "registry.example.com/acme/api:latest",
      name: "api",
      targetPort: 8080,
    },
    (_input, init) => {
      requestInit = init;
      return Promise.resolve(
        Response.json(
          {
            createdAt: 1,
            enabled: true,
            environment: { APP_ENV: "production" },
            id: "service",
            imageCredentialId: "credential",
            imageReference: "registry.example.com/acme/api:latest",
            name: "api",
            projectId: "project",
            secretReferences: [],
            startupTimeoutSeconds: 60,
            targetPort: 8080,
            updatedAt: 1,
            volumeMounts: [],
          },
          { status: 201 }
        )
      );
    }
  );
  expect(service.id).toBe("service");
  expect(requestInit?.method).toBe("POST");
});

test("reads and mutates service lifecycle with optimistic version fields", async () => {
  const service = {
    createdAt: 1,
    enabled: true,
    environment: {},
    id: "service",
    imageReference: "docker.io/library/alpine:latest",
    name: "api",
    projectId: "project",
    secretReferences: [],
    startupTimeoutSeconds: 60,
    updatedAt: 2,
    volumeMounts: [],
  };
  await expect(
    fetchService("project", "service", undefined, () =>
      Promise.resolve(Response.json(service))
    )
  ).resolves.toEqual(service);

  let updateBody = "";
  await updateService(
    "project",
    "service",
    {
      enabled: false,
      environment: {},
      expectedUpdatedAt: 2,
      imageReference: service.imageReference,
      secretReferences: [],
      startupTimeoutSeconds: 60,
      volumeMounts: [],
    },
    (_input, init) => {
      updateBody = init?.body?.toString() ?? "";
      return Promise.resolve(
        Response.json({ ...service, enabled: false, updatedAt: 3 })
      );
    }
  );
  expect(JSON.parse(updateBody)).toEqual({
    enabled: false,
    environment: {},
    expectedUpdatedAt: 2,
    imageReference: service.imageReference,
    secretReferences: [],
    startupTimeoutSeconds: 60,
    volumeMounts: [],
  });

  await expect(
    redeployService("project", "service", 2, () =>
      Promise.resolve(Response.json(service))
    )
  ).resolves.toEqual(service);
  await expect(
    rollbackService("project", "service", "deployment", 2, () =>
      Promise.resolve(Response.json({ ...service, updatedAt: 3 }))
    )
  ).resolves.toMatchObject({ updatedAt: 3 });
});

test("validates bounded deployment history pages", async () => {
  await expect(
    fetchServiceDeployments("project", "service", undefined, undefined, () =>
      Promise.resolve(
        Response.json({
          deployments: [
            {
              createdAt: 1,
              id: "deployment",
              imageDigest: "sha256:image",
              serviceConfigHash: "config",
              serviceId: "service",
              snapshot: {
                environment: {},
                imageReference: "docker.io/library/alpine:latest",
                secretReferences: [],
                startupTimeoutSeconds: 60,
                volumeMounts: [],
              },
              status: "succeeded",
            },
          ],
          nextCursor: "deployment",
        })
      )
    )
  ).resolves.toMatchObject({ nextCursor: "deployment" });
});
