import { ArrowDownToLine, ArrowUpFromLine, Cloud, Network } from "lucide-react";

import type {
  HostNetworkAddress,
  NetworkGatewayInput,
  ProjectCanvas,
} from "@/api";
import { CloudflareMeshConnection } from "@/cloudflare-mesh-connection";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ContainerPortCombobox } from "@/container-port-combobox";
import { cn } from "@/lib/utils";
import { resetNetworkGatewayDirection } from "@/network-gateway-form-model";
import { useContainerPorts } from "@/use-container-ports";

const portValue = (value: string) => (value === "" ? 0 : Number(value));

const modeOptions = [
  {
    description:
      "Give a remote VPC or Mesh endpoint a project .internal address.",
    icon: ArrowDownToLine,
    label: "Import endpoint",
    value: "import" as const,
  },
  {
    description:
      "Publish one platformd service port on a private network address.",
    icon: ArrowUpFromLine,
    label: "Export service",
    value: "export" as const,
  },
];

export const NetworkGatewayForm = ({
  addresses,
  className,
  input,
  onChange,
  onMeshConfiguredChange,
  projectID,
  resources,
}: {
  addresses: HostNetworkAddress[];
  className?: string;
  input: NetworkGatewayInput;
  onChange: (input: NetworkGatewayInput) => void;
  onMeshConfiguredChange?: (configured: boolean) => void;
  projectID: string;
  resources: ProjectCanvas["resources"];
}) => {
  const services = resources.filter(
    (resource) =>
      resource.kind === "service" && !resource.id.startsWith("draft:")
  );
  const selectedAddress = input.interfaceName
    ? `${input.interfaceName}|${input.sourceAddress}`
    : "";
  const containerPorts = useContainerPorts(
    projectID,
    "service",
    input.targetServiceId,
    input.mode === "export" && input.targetServiceId !== ""
  );

  return (
    <div
      className={cn("divide-y divide-border border border-border", className)}
    >
      <section className="grid md:grid-cols-[14rem_minmax(0,1fr)]">
        <div className="px-4 py-4">
          <p className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Direction
          </p>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            The proxy always runs on this platformd host.
          </p>
        </div>
        <div className="grid border-t border-border md:grid-cols-2 md:border-t-0 md:border-l">
          {modeOptions.map((option) => {
            const Icon = option.icon;
            const selected = input.mode === option.value;
            return (
              <button
                className={cn(
                  "flex min-h-24 items-start gap-3 px-4 py-4 text-left transition-colors first:border-b first:border-border md:first:border-r md:first:border-b-0",
                  selected
                    ? "bg-muted/60 text-foreground"
                    : "text-muted-foreground hover:bg-muted/30"
                )}
                key={option.value}
                onClick={() => {
                  if (option.value !== input.mode) {
                    onChange(
                      resetNetworkGatewayDirection(
                        input,
                        option.value,
                        addresses
                      )
                    );
                  }
                }}
                type="button"
              >
                <Icon className="mt-0.5 size-4 shrink-0" />
                <span>
                  <span className="block text-[10px] font-medium text-foreground">
                    {option.label}
                  </span>
                  <span className="mt-1 block text-[9px] leading-4">
                    {option.description}
                  </span>
                </span>
              </button>
            );
          })}
        </div>
      </section>

      <section className="grid md:grid-cols-[14rem_minmax(0,1fr)]">
        <div className="px-4 py-4">
          <p className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Private network
          </p>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            Select a VPC address or connect this installation to Cloudflare
            Mesh.
          </p>
        </div>
        <div className="grid border-t border-border sm:grid-cols-2 md:border-t-0 md:border-l">
          <label
            className="border-b border-border px-4 py-4 sm:border-r"
            htmlFor="gateway-name"
          >
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Resource name
            </span>
            <Input
              autoCapitalize="none"
              autoComplete="off"
              className="mt-2"
              id="gateway-name"
              onChange={(event) =>
                onChange({ ...input, name: event.target.value })
              }
              placeholder="warehouse-db"
              required
              spellCheck={false}
              value={input.name}
            />
          </label>
          <div className="border-b border-border px-4 py-4">
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Network type
            </span>
            <Select
              onValueChange={(transport) => {
                const selected = transport as "mesh" | "vpc";
                const [firstAddress] = addresses;
                onChange({
                  ...input,
                  interfaceName:
                    selected === "mesh"
                      ? ""
                      : input.interfaceName || firstAddress?.interface || "",
                  sourceAddress:
                    selected === "mesh"
                      ? ""
                      : input.sourceAddress || firstAddress?.address || "",
                  transport: selected,
                });
              }}
              value={input.transport}
            >
              <SelectTrigger className="mt-2 w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent align="start" alignItemWithTrigger={false}>
                <SelectItem value="vpc">
                  <Network /> VPC
                </SelectItem>
                <SelectItem value="mesh">
                  <Cloud /> Cloudflare Mesh
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          {input.transport === "mesh" ? (
            <CloudflareMeshConnection
              onConfiguredChange={onMeshConfiguredChange}
            />
          ) : (
            <div className="px-4 py-4 sm:col-span-2">
              <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                Host interface and address
              </span>
              <Select
                onValueChange={(value) => {
                  if (!value) {
                    return;
                  }
                  const [interfaceName = "", sourceAddress = ""] =
                    value.split("|");
                  onChange({ ...input, interfaceName, sourceAddress });
                }}
                value={selectedAddress || null}
              >
                <SelectTrigger className="mt-2 w-full">
                  <SelectValue placeholder="Select a host address" />
                </SelectTrigger>
                <SelectContent align="start" alignItemWithTrigger={false}>
                  {addresses.map((address) => (
                    <SelectItem
                      key={`${address.interface}|${address.address}`}
                      value={`${address.interface}|${address.address}`}
                    >
                      <span className="font-mono">{address.interface}</span>
                      <span className="text-muted-foreground">
                        {address.address}
                      </span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {addresses.length === 0 ? (
                <p className="mt-2 text-[9px] text-amber-600 dark:text-amber-300">
                  No non-loopback IPv4 addresses are currently available.
                </p>
              ) : null}
            </div>
          )}
        </div>
      </section>

      <section className="grid md:grid-cols-[14rem_minmax(0,1fr)]">
        <div className="px-4 py-4">
          <p className="text-[9px] tracking-[0.13em] text-muted-foreground uppercase">
            Port mapping
          </p>
          <p className="mt-2 text-[9px] leading-4 text-muted-foreground">
            One network gateway owns exactly one TCP or UDP mapping.
          </p>
        </div>
        <div className="grid border-t border-border sm:grid-cols-2 md:border-t-0 md:border-l">
          <div className="border-b border-border px-4 py-4 sm:border-r">
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              Protocol
            </span>
            <Select
              onValueChange={(protocol) =>
                onChange({
                  ...input,
                  protocol: protocol as "tcp" | "udp",
                  targetPort: input.mode === "export" ? 0 : input.targetPort,
                })
              }
              value={input.protocol}
            >
              <SelectTrigger className="mt-2 w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent align="start" alignItemWithTrigger={false}>
                <SelectItem value="tcp">TCP</SelectItem>
                <SelectItem value="udp">UDP</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <label
            className="border-b border-border px-4 py-4"
            htmlFor="gateway-listen-port"
          >
            <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
              {input.mode === "import"
                ? "Internal port"
                : "Private network port"}
            </span>
            <Input
              className="mt-2"
              id="gateway-listen-port"
              max={65_535}
              min={1}
              onChange={(event) =>
                onChange({
                  ...input,
                  listenPort: portValue(event.target.value),
                })
              }
              placeholder="5432"
              required
              type="number"
              value={input.listenPort || ""}
            />
          </label>
          {input.mode === "import" ? (
            <>
              <label
                className="px-4 py-4 sm:border-r"
                htmlFor="gateway-remote-host"
              >
                <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                  Remote host
                </span>
                <Input
                  autoCapitalize="none"
                  autoComplete="off"
                  className="mt-2"
                  id="gateway-remote-host"
                  onChange={(event) =>
                    onChange({ ...input, remoteHost: event.target.value })
                  }
                  placeholder="10.24.0.12"
                  required
                  spellCheck={false}
                  value={input.remoteHost}
                />
              </label>
              <label className="px-4 py-4" htmlFor="gateway-remote-port">
                <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                  Remote port
                </span>
                <Input
                  className="mt-2"
                  id="gateway-remote-port"
                  max={65_535}
                  min={1}
                  onChange={(event) =>
                    onChange({
                      ...input,
                      remotePort: portValue(event.target.value),
                    })
                  }
                  placeholder="5432"
                  required
                  type="number"
                  value={input.remotePort || ""}
                />
              </label>
            </>
          ) : (
            <>
              <div className="px-4 py-4 sm:border-r">
                <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                  Target service
                </span>
                <Select
                  onValueChange={(targetServiceId) => {
                    if (targetServiceId) {
                      onChange({ ...input, targetPort: 0, targetServiceId });
                    }
                  }}
                  value={input.targetServiceId || null}
                >
                  <SelectTrigger className="mt-2 w-full">
                    <SelectValue placeholder="Select a service" />
                  </SelectTrigger>
                  <SelectContent align="start" alignItemWithTrigger={false}>
                    {services.map((service) => (
                      <SelectItem key={service.id} value={service.id}>
                        {service.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="px-4 py-4">
                <span className="text-[8px] tracking-[0.12em] text-muted-foreground uppercase">
                  Container port
                </span>
                <ContainerPortCombobox
                  ariaLabel="Gateway target container port"
                  className="mt-2"
                  disabled={!input.targetServiceId}
                  onChange={(targetPort) => onChange({ ...input, targetPort })}
                  placeholder="8080"
                  ports={containerPorts.ports}
                  protocol={input.protocol}
                  status={containerPorts.status}
                  value={input.targetPort}
                />
              </div>
            </>
          )}
        </div>
      </section>
    </div>
  );
};
