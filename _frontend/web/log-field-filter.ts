import { z } from "zod";

export const logFieldFilterOperatorSchema = z.enum([
  "equals",
  "contains",
  "exists",
]);

export const logFieldFilterSchema = z.object({
  operator: logFieldFilterOperatorSchema,
  path: z
    .string()
    .min(1)
    .max(256)
    .regex(/^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*$/u),
  value: z.string().max(512).default(""),
});

export const logFieldFiltersSchema = z.array(logFieldFilterSchema).max(8);

export type LogFieldFilter = z.infer<typeof logFieldFilterSchema>;
export type LogFieldFilterOperator = z.infer<
  typeof logFieldFilterOperatorSchema
>;

const reservedFields = new Set([
  "level",
  "message",
  "msg",
  "severity",
  "severity_text",
  "severityText",
  "span_id",
  "spanId",
  "trace_id",
  "traceId",
]);

const scalar = (value: unknown): value is boolean | number | string | null =>
  value === null || ["boolean", "number", "string"].includes(typeof value);

export const structuredLogFields = (
  fields: Record<string, unknown>,
  limit = 8
): { path: string; value: string }[] => {
  const result: { path: string; value: string }[] = [];
  const visit = (value: unknown, path: string, depth: number) => {
    if (result.length >= limit) {
      return;
    }
    if (scalar(value)) {
      if (path && !reservedFields.has(path)) {
        result.push({ path, value: value === null ? "null" : String(value) });
      }
      return;
    }
    if (
      depth >= 4 ||
      Array.isArray(value) ||
      typeof value !== "object" ||
      value === null
    ) {
      return;
    }
    for (const [key, child] of Object.entries(value)) {
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/u.test(key)) {
        continue;
      }
      visit(child, path ? `${path}.${key}` : key, depth + 1);
    }
  };
  visit(fields, "", 0);
  return result;
};
