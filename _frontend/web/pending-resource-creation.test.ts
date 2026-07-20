import { expect, test } from "bun:test";

import {
  emptyPendingBackupPolicy,
  emptyPendingServiceCreationSettings,
  pendingResourceChangeDetails,
} from "@/pending-resource-creation";
import type { PendingResourceCreation } from "@/pending-resource-creation";

test("counts every dependency configured on a pending service", () => {
  const input = {
    environment: {},
    name: "api",
    source: {
      autoUpdate: true,
      image: { reference: "docker.io/library/nginx:stable" },
      type: "public_image" as const,
    },
  };
  const settings = emptyPendingServiceCreationSettings(input);
  const draft: PendingResourceCreation = {
    id: "draft:service",
    input,
    kind: "service",
    settings: {
      ...settings,
      domains: [{ hostname: "api.example.com", targetPort: 8080 }],
      listeners: [{ protocol: "tcp", publicPort: 9000, targetPort: 8080 }],
      volumeMounts: [{ containerPath: "/data", volumeId: "draft:volume" }],
      volumes: [
        {
          createdAt: 1,
          id: "draft:volume",
          name: "data",
          ownerGid: 1001,
          ownerUid: 1000,
          pendingCreation: true,
          projectId: "project",
          serviceId: "draft:service",
        },
      ],
    },
  };

  expect(pendingResourceChangeDetails(draft)).toEqual([
    {
      detail: "Service",
      id: "create:draft:service",
      label: "Create resource",
    },
    {
      detail: "api.example.com → :8080",
      id: "domain:api.example.com",
      label: "Add domain",
    },
    {
      detail: "TCP :9000 → :8080",
      id: "listener:tcp:9000",
      label: "Add listener",
    },
    {
      detail: "data → /data",
      id: "volume:draft:volume",
      label: "Add volume",
    },
  ]);
});

test("counts an enabled backup policy on a managed resource draft", () => {
  const backupPolicy = {
    ...emptyPendingBackupPolicy(),
    enabled: true,
    targetId: "backup-target",
  };
  const draft: PendingResourceCreation = {
    backupPolicy,
    id: "draft:redis",
    input: {
      credentials: { password: "draft-password" },
      imageTag: "8.2",
      name: "cache",
    },
    kind: "redis",
  };

  expect(pendingResourceChangeDetails(draft)).toEqual([
    {
      detail: "Redis",
      id: "create:draft:redis",
      label: "Create resource",
    },
    {
      detail: "0 3 * * * · keep 7",
      id: "backups:draft:redis",
      label: "Configure backups",
    },
  ]);
});

test("counts a network gateway draft as one independently deployable resource", () => {
  const draft: PendingResourceCreation = {
    id: "draft:gateway",
    input: {
      interfaceName: "tailscale0",
      listenPort: 5432,
      mode: "import",
      name: "warehouse-db",
      protocol: "tcp",
      remoteHost: "100.64.0.20",
      remotePort: 5432,
      sourceAddress: "100.64.0.10",
      targetPort: 0,
      targetServiceId: "",
      transport: "mesh",
    },
    kind: "network_gateway",
  };

  expect(pendingResourceChangeDetails(draft)).toEqual([
    {
      detail: "Network gateway",
      id: "create:draft:gateway",
      label: "Create resource",
    },
  ]);
});
