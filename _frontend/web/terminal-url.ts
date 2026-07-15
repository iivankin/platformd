import type { ContainerResourceKind } from "@/api";

export const resourceTerminalSocketURL = (
  projectID: string,
  resourceKind: ContainerResourceKind,
  resourceID: string,
  command: string[],
  cols: number,
  rows: number,
  origin = window.location.origin
) => {
  const url = new URL(
    `/api/v1/projects/${encodeURIComponent(projectID)}/resources/${encodeURIComponent(resourceKind)}/${encodeURIComponent(resourceID)}/terminal`,
    origin
  );
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("cols", String(Math.max(1, Math.min(1000, cols))));
  url.searchParams.set("rows", String(Math.max(1, Math.min(500, rows))));
  for (const argument of command) {
    url.searchParams.append("arg", argument);
  }
  return url.toString();
};

export const serverTerminalSocketURL = (
  cols: number,
  rows: number,
  origin = window.location.origin
) => {
  const url = new URL("/api/v1/server/terminal", origin);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("cols", String(Math.max(1, Math.min(1000, cols))));
  url.searchParams.set("rows", String(Math.max(1, Math.min(500, rows))));
  return url.toString();
};
