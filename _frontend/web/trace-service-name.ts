import type { ServiceTraceSpan } from "@/api";
import { otlpAttribute } from "@/otlp";

export type ServiceNameResolver = (serviceID: string) => string | undefined;

export const isSyntheticServiceName = (name: string) =>
  name === "unknown_service" || name.startsWith("unknown_service:");

export const resolvedServiceName = (
  serviceID: string,
  resourceName: unknown,
  serviceName?: ServiceNameResolver
) => {
  if (
    typeof resourceName === "string" &&
    resourceName !== "" &&
    !isSyntheticServiceName(resourceName)
  ) {
    return resourceName;
  }
  return serviceName?.(serviceID) ?? serviceID;
};

export const traceServiceName = (
  span: ServiceTraceSpan,
  serviceName?: ServiceNameResolver
) =>
  resolvedServiceName(
    span.serviceId,
    otlpAttribute(span.resource, "service.name"),
    serviceName
  );
