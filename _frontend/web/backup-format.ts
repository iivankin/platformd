import type { BackupRecord } from "@/api";

export const formatBackupBytes = (value?: number) => {
  if (value === undefined) {
    return "—";
  }
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let amount = value;
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
};

export const formatBackupTimestamp = (value?: number) =>
  value
    ? new Date(value).toLocaleString(undefined, {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : "—";

export const formatBackupDuration = (record: BackupRecord) => {
  if (!record.finishedAt || record.finishedAt < record.startedAt) {
    return;
  }
  const seconds = Math.round((record.finishedAt - record.startedAt) / 1000);
  return seconds < 60
    ? `${seconds}s`
    : `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
};
