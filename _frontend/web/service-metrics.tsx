import { CustomMetrics } from "@/custom-metrics";
import { ResourceUsage } from "@/resource-usage";

export const ServiceMetrics = ({
  cpuMillicores,
  memoryBytes,
  projectID,
  serviceID,
}: {
  cpuMillicores?: number;
  memoryBytes?: number;
  projectID: string;
  serviceID: string;
}) => (
  <div className="space-y-4 p-4 lg:p-6">
    <header className="border-b border-border pb-4">
      <h2 className="text-sm font-medium">Metrics</h2>
      <p className="mt-1 text-[9px] text-muted-foreground">
        Runtime usage and application metrics for this service.
      </p>
    </header>
    <ResourceUsage
      cpuMillicores={cpuMillicores}
      kind="service"
      memoryBytes={memoryBytes}
      resourceID={serviceID}
    />
    <CustomMetrics scope={{ kind: "service", projectID, serviceID }} />
  </div>
);
