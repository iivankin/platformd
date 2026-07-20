import { expect, test } from "bun:test";

import {
  emptyNetworkGatewayInput,
  resetNetworkGatewayDirection,
} from "@/network-gateway-form-model";

test("switching gateway direction clears incompatible configuration", () => {
  const reset = resetNetworkGatewayDirection(
    {
      ...emptyNetworkGatewayInput(),
      interfaceName: "CloudflareWARP",
      listenPort: 5432,
      mode: "import",
      name: "warehouse-db",
      protocol: "udp",
      remoteHost: "100.96.0.12",
      remotePort: 5432,
      sourceAddress: "100.96.0.10",
      targetPort: 8080,
      targetServiceId: "service-a",
      transport: "mesh",
    },
    "export",
    [{ address: "10.24.0.10", interface: "wg-vpc" }]
  );

  expect(reset).toEqual({
    ...emptyNetworkGatewayInput(),
    interfaceName: "wg-vpc",
    mode: "export",
    name: "warehouse-db",
    sourceAddress: "10.24.0.10",
  });
});
