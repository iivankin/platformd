import { expect, test } from "bun:test";

import type { BackupGeneration, BackupRecord } from "@/api";
import { recentBackupItems } from "@/backup-list";

test("combines backup runs and restorable generations without duplicates", () => {
  const history: BackupRecord[] = [
    {
      finishedAt: 200,
      generationId: "current",
      id: "run-current",
      resourceId: "resource",
      resourceKind: "registry",
      startedAt: 100,
      status: "succeeded",
      targetId: "target",
    },
    {
      finishedAt: 300,
      id: "run-failed",
      resourceId: "resource",
      resourceKind: "registry",
      startedAt: 250,
      status: "failed",
      targetId: "target",
    },
  ];
  const generations: BackupGeneration[] = [
    {
      completedAt: 200,
      generationId: "current",
      plaintextSize: 20,
      remoteSize: 10,
    },
    {
      completedAt: 50,
      generationId: "older-than-history",
      plaintextSize: 20,
      remoteSize: 10,
    },
  ];

  const items = recentBackupItems(history, generations);
  expect(items).toHaveLength(3);
  expect(items.map((item) => item.key)).toEqual([
    "run-run-failed",
    "run-run-current",
    "generation-older-than-history",
  ]);
  expect(items[1]?.generation?.generationId).toBe("current");
});
