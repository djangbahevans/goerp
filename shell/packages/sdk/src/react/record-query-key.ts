import type { QueryKey } from "@tanstack/react-query";

export function recordQueryKey(resource: string, id: string | undefined): QueryKey {
  return ["record", resource, id ?? null];
}
