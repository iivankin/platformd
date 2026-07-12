import { expect, test } from "bun:test";

import {
  APIError,
  attachServiceDomain,
  createAPIToken,
  createProject,
  createImageCredential,
  createService,
  detachServiceDomain,
  fetchAPITokens,
  fetchAuditEvents,
  fetchService,
  fetchServiceDeployments,
  fetchServiceDomains,
  fetchServiceLogs,
  fetchImageCredentials,
  fetchIdentity,
  fetchMeta,
  fetchProjectCanvas,
  fetchProjects,
  redeployService,
  revokeAPIToken,
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

test("reads a validated bounded structured log window", async () => {
  let requested = "";
  await expect(
    fetchServiceLogs(
      "project",
      "service",
      { contains: "ready", deploymentId: "deployment", limit: 25 },
      undefined,
      (input) => {
        requested = input.toString();
        return Promise.resolve(
          Response.json({
            records: [
              {
                attemptId: "attempt",
                deploymentId: "deployment",
                stream: "stdout",
                text: "ready",
                timestamp: "2026-07-12T10:00:00.000000001Z",
              },
            ],
            truncated: false,
          })
        );
      }
    )
  ).resolves.toMatchObject({ records: [{ text: "ready" }] });
  expect(requested).toBe(
    "/api/v1/projects/project/services/service/logs?limit=25&deploymentId=deployment&contains=ready"
  );
});

test("reads filtered paginated audit history", async () => {
  let requested = "";
  await expect(
    fetchAuditEvents(
      {
        action: "server.exec",
        actorKind: "token",
        cursor: "cursor",
        limit: 25,
        result: "succeeded",
      },
      undefined,
      (input) => {
        requested = input.toString();
        return Promise.resolve(
          Response.json({
            events: [
              {
                action: "server.exec",
                actorId: "token",
                actorKind: "token",
                createdAt: 20,
                id: "event",
                metadata: { durationMillis: 10 },
                result: "succeeded",
                targetId: "host",
                targetKind: "server",
              },
            ],
          })
        );
      }
    )
  ).resolves.toMatchObject({ events: [{ action: "server.exec" }] });
  expect(requested).toBe(
    "/api/v1/audit?limit=25&action=server.exec&actorKind=token&cursor=cursor&result=succeeded"
  );
});

test("lists, attaches, moves, and detaches exact service domains", async () => {
  const domain = {
    createdAt: 1,
    hostname: "api.example.com",
    projectId: "project",
    projectName: "shop",
    serviceId: "service",
    serviceName: "api",
  };
  await expect(
    fetchServiceDomains("project", "service", undefined, () =>
      Promise.resolve(Response.json({ domains: [domain] }))
    )
  ).resolves.toEqual([domain]);

  let attachBody = "";
  await expect(
    attachServiceDomain(
      "project",
      "service",
      domain.hostname,
      true,
      (_input, init) => {
        attachBody = init?.body?.toString() ?? "";
        return Promise.resolve(Response.json(domain, { status: 201 }));
      }
    )
  ).resolves.toEqual(domain);
  expect(JSON.parse(attachBody)).toEqual({
    hostname: domain.hostname,
    move: true,
  });

  let detachURL = "";
  let detachMethod = "";
  await detachServiceDomain(
    "project/one",
    "service/two",
    domain.hostname,
    (input, init) => {
      detachURL = input.toString();
      detachMethod = init?.method ?? "";
      return Promise.resolve(new Response(null, { status: 204 }));
    }
  );
  expect(detachURL).toBe(
    "/api/v1/projects/project%2Fone/services/service%2Ftwo/domains/api.example.com"
  );
  expect(detachMethod).toBe("DELETE");

  const conflict = await attachServiceDomain(
    "project",
    "service",
    domain.hostname,
    false,
    () =>
      Bun.sleep(0).then(() =>
        Response.json(
          {
            error: {
              code: "domain_conflict",
              domain,
              message: "Domain belongs to another service",
            },
          },
          { status: 409 }
        )
      )
  ).catch((error: unknown) => error);
  expect(conflict).toBeInstanceOf(APIError);
  expect((conflict as APIError).domain).toEqual(domain);
});

test("creates and revokes one-time API tokens", async () => {
  const token = {
    createdAt: 1,
    id: "token-id",
    name: "deploy-bot",
    projectId: "project",
    role: "admin" as const,
  };
  await expect(
    fetchAPITokens(undefined, () =>
      Promise.resolve(Response.json({ tokens: [token] }))
    )
  ).resolves.toEqual([token]);

  let createBody = "";
  await expect(
    createAPIToken(
      { name: token.name, projectId: token.projectId, role: token.role },
      (_input, init) => {
        createBody = init?.body?.toString() ?? "";
        return Promise.resolve(
          Response.json({ ...token, token: "ptk_token-id_secret" })
        );
      }
    )
  ).resolves.toMatchObject({ token: "ptk_token-id_secret" });
  expect(JSON.parse(createBody)).toEqual({
    name: token.name,
    projectId: token.projectId,
    role: token.role,
  });

  let revokeURL = "";
  await revokeAPIToken("token/with slash", (input, init) => {
    revokeURL = input.toString();
    expect(init?.method).toBe("DELETE");
    return Promise.resolve(new Response(null, { status: 204 }));
  });
  expect(revokeURL).toBe("/api/v1/tokens/token%2Fwith%20slash");
});
