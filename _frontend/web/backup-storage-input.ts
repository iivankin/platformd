import type { SetBackupTargetInput } from "@/api";

export const emptyBackupTargetInput: SetBackupTargetInput = {
  accessKeyId: "",
  bucket: "",
  endpoint: "",
  name: "",
  prefix: "",
  region: "",
  secretAccessKey: "",
};

export const completeBackupTargetInput = (input: SetBackupTargetInput) =>
  Boolean(
    input.name.trim() &&
    input.endpoint.trim() &&
    input.region.trim() &&
    input.bucket.trim() &&
    input.accessKeyId.trim() &&
    input.secretAccessKey
  );

export const normalizeBackupTargetInput = (
  input: SetBackupTargetInput
): SetBackupTargetInput => ({
  ...input,
  accessKeyId: input.accessKeyId.trim(),
  bucket: input.bucket.trim(),
  endpoint: input.endpoint.trim(),
  name: input.name.trim(),
  prefix: input.prefix.trim(),
  region: input.region.trim(),
});
