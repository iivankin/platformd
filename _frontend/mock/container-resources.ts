import { apiSegments, json, mockError, noContent } from "./http";
import type { MockState } from "./state";

const keyFor = (kind: string, resourceID: string) => `${kind}:${resourceID}`;

const knownResource = (state: MockState, kind: string, resourceID: string) => {
  if (kind === "service") {
    return Boolean(state.services[resourceID]);
  }
  if (kind === "postgres") {
    return Boolean(state.postgres[resourceID]);
  }
  if (kind === "redis") {
    return Boolean(state.redis[resourceID]);
  }
  return false;
};

const entriesBelow = (files: Record<string, string>, root: string) => {
  const directoryPaths = new Set<string>();
  const prefix = root === "/" ? "/" : `${root}/`;
  for (const filePath of Object.keys(files)) {
    if (!filePath.startsWith(prefix)) {
      continue;
    }
    const parts = filePath.slice(prefix.length).split("/");
    for (let index = 1; index < parts.length; index += 1) {
      directoryPaths.add(
        root === "/"
          ? `/${parts.slice(0, index).join("/")}`
          : `${root}/${parts.slice(0, index).join("/")}`
      );
    }
  }
  const modifiedAt = new Date(1_752_499_200_000).toISOString();
  return [
    ...[...directoryPaths].map((path) => ({
      directory: true,
      mode: 0o755,
      modifiedAt,
      path,
      sizeBytes: 0,
    })),
    ...Object.entries(files)
      .filter(([path]) => path.startsWith(prefix))
      .map(([path, content]) => ({
        directory: false,
        mode: 0o640,
        modifiedAt,
        path,
        sizeBytes: new TextEncoder().encode(content).byteLength,
      })),
  ].toSorted((left, right) => left.path.localeCompare(right.path));
};

const handleFiles = async (
  request: Request,
  files: Record<string, string>,
  url: URL
): Promise<Response | undefined> => {
  const requestedPath = url.searchParams.get("path") ?? "/";
  const contentRequest = url.pathname.endsWith("/content");
  if (request.method === "GET" && !contentRequest) {
    return json({
      entries: entriesBelow(files, requestedPath),
      root: requestedPath,
    });
  }
  if (request.method === "GET") {
    const content = files[requestedPath];
    return content
      ? new Response(content, {
          headers: {
            "Content-Disposition": `attachment; filename="${requestedPath.split("/").at(-1) ?? "file"}"`,
            "Content-Type": "application/octet-stream",
          },
        })
      : mockError("not_found", "Mock container file not found", 404);
  }
  if (request.method !== "PUT") {
    return undefined;
  }
  files[requestedPath] = await request.text();
  return noContent();
};

export const handleContainerResourcesAPI = async (
  request: Request,
  state: MockState,
  pathname: string,
  url: URL
): Promise<Response | undefined> => {
  const [
    root,
    projectID,
    collection,
    kind,
    resourceID,
    resource,
    detail,
    ...tail
  ] = apiSegments(pathname);
  const validPrefix = [
    root === "projects",
    Boolean(projectID),
    collection === "resources",
    Boolean(kind),
    Boolean(resourceID),
    tail.length === 0,
  ].every(Boolean);
  if (!(validPrefix && knownResource(state, kind ?? "", resourceID ?? ""))) {
    return undefined;
  }
  if (
    request.method === "GET" &&
    resource === "terminal" &&
    detail === "shells"
  ) {
    return json({ shells: ["/bin/sh", "/bin/bash"] });
  }
  const key = keyFor(kind ?? "", resourceID ?? "");
  if (request.method === "GET" && resource === "ports" && !detail) {
    return json({ ports: state.containerPorts[key] ?? [] });
  }
  if (resource !== "files" || (detail && detail !== "content")) {
    return undefined;
  }
  const files = state.containerFiles[key] ?? {};
  const response = await handleFiles(request, files, url);
  state.containerFiles[key] = files;
  return response;
};

export const mockContainerFiles = (kind: string): Record<string, string> => {
  if (kind === "service") {
    return {
      "/app/README.md": "Mock service container\n",
      "/app/config/runtime.json": '{"port":8080}\n',
      "/app/package.json": '{"name":"storefront-api"}\n',
    };
  }
  if (kind === "postgres") {
    return {
      "/var/lib/postgresql/data/PG_VERSION": "17\n",
      "/var/lib/postgresql/data/postgresql.auto.conf":
        "# Mock PostgreSQL configuration\n",
    };
  }
  return {
    "/data/README": "Mock Redis volume\n",
    "/data/redis.conf": "appendonly yes\n",
  };
};
