import type { HostNetworkAddress, NetworkGatewayInput } from "@/api";

export const emptyNetworkGatewayInput = (): NetworkGatewayInput => ({
  interfaceName: "",
  listenPort: 0,
  mode: "import",
  name: "",
  protocol: "tcp",
  remoteHost: "",
  remotePort: 0,
  sourceAddress: "",
  targetPort: 0,
  targetServiceId: "",
  transport: "vpc",
});

export const resetNetworkGatewayDirection = (
  input: NetworkGatewayInput,
  mode: NetworkGatewayInput["mode"],
  addresses: HostNetworkAddress[]
): NetworkGatewayInput => {
  const [firstAddress] = addresses;
  return {
    ...emptyNetworkGatewayInput(),
    interfaceName: firstAddress?.interface ?? "",
    mode,
    // The resource identity is independent from the proxy direction.
    name: input.name,
    sourceAddress: firstAddress?.address ?? "",
  };
};
