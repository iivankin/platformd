import { z } from "zod";

const metaSchema = z.object({
  architecture: z.string(),
  os: z.string(),
  status: z.enum(["bootstrapping", "ready", "recovery"]),
  version: z.string(),
});

export type Meta = z.infer<typeof metaSchema>;

type Fetcher = (
  input: RequestInfo | URL,
  init?: RequestInit
) => Promise<Response>;

export const fetchMeta = async (
  signal?: AbortSignal,
  fetcher: Fetcher = globalThis.fetch
): Promise<Meta> => {
  const response = await fetcher("/api/v1/meta", {
    headers: { Accept: "application/json" },
    signal,
  });

  if (!response.ok) {
    throw new Error(`meta request failed with ${response.status}`);
  }

  return metaSchema.parse(await response.json());
};
