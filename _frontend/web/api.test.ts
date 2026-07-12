import { expect, test } from "bun:test";

import {
  createProject,
  fetchIdentity,
  fetchMeta,
  fetchProjectCanvas,
  fetchProjects,
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
            },
          ],
        })
      );
    }
  );
  expect(requestURL).toBe("/api/v1/projects/project%2Fwith%20slash/canvas");
  expect(canvas.connections[0]?.environmentNames).toEqual(["DATABASE_URL"]);
});
