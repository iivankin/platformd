export const serviceTerminalSocketURL = (
  projectID: string,
  serviceID: string,
  command: string[],
  cols: number,
  rows: number,
  origin = window.location.origin
) => {
  const url = new URL(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/terminal`,
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
