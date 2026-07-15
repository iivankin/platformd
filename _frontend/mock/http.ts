export const json = (value: unknown, status = 200) =>
  Response.json(value, { status });

export const noContent = () => new Response(null, { status: 204 });

export const mockError = (code: string, message: string, status = 400) =>
  json({ error: { code, message } }, status);

export const apiSegments = (pathname: string) =>
  pathname
    .slice("/api/v1/".length)
    .split("/")
    .map((segment) => decodeURIComponent(segment));

export const readObject = async (
  request: Request
): Promise<Record<string, unknown>> => {
  const value: unknown = await request.json().catch(() => null);
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
};

export const stringField = (
  object: Record<string, unknown>,
  field: string,
  fallback = ""
) => (typeof object[field] === "string" ? object[field] : fallback);

export const booleanField = (
  object: Record<string, unknown>,
  field: string,
  fallback = false
) => (typeof object[field] === "boolean" ? object[field] : fallback);

export const numberField = (
  object: Record<string, unknown>,
  field: string,
  fallback: number
) => (typeof object[field] === "number" ? object[field] : fallback);
