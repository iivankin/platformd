import type { LogStreamMessage, LogWindow } from "@/api";

export const serviceLogSocketURL = (
  projectID: string,
  serviceID: string,
  filters: { contains?: string; deploymentId?: string; limit: number },
  origin = window.location.origin
) => {
  const url = new URL(
    `/api/v1/projects/${encodeURIComponent(projectID)}/services/${encodeURIComponent(serviceID)}/logs/stream`,
    origin
  );
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.searchParams.set("limit", String(filters.limit));
  if (filters.deploymentId) {
    url.searchParams.set("deploymentId", filters.deploymentId);
  }
  if (filters.contains) {
    url.searchParams.set("contains", filters.contains);
  }
  return url.toString();
};

export const applyLogStreamMessage = (
  current: LogWindow | undefined,
  message: LogStreamMessage,
  limit: number
): LogWindow => {
  if (message.type === "snapshot") {
    return {
      records: message.records.slice(-limit),
      truncated: message.truncated,
    };
  }
  if (message.type === "gap") {
    return { records: current?.records ?? [], truncated: true };
  }
  const records = [...(current?.records ?? []), ...message.records];
  const overflow = records.length > limit;
  return {
    records: records.slice(-limit),
    truncated: Boolean(current?.truncated) || overflow,
  };
};
