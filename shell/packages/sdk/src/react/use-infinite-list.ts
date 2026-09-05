import { type InfiniteData, type QueryKey, useInfiniteQuery } from "@tanstack/react-query";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

// typescript-sdk-reference.md §5 `useInfiniteList` — resource-name-keyed
// cursor pagination shared by every module's list views, resolving where
// to fetch from via the resource registry (goerp#638) instead of taking
// a path directly.
export interface UseInfiniteListOptions {
  filter?: Record<string, unknown>;
  sort?: string;
  limit?: number;
}

// `filter: { is_active: true }` becomes `filter[is_active]=true` on the
// wire — typescript-sdk-reference.md's ListContactsParams bracket-key
// convention; FetchAPIClient's buildURL sends whatever flat keys it's given.
function flattenFilter(filter: Record<string, unknown> | undefined): Record<string, unknown> {
  const flat: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(filter ?? {})) {
    flat[`filter[${key}]`] = value;
  }
  return flat;
}

export function createInfiniteListQueryOptions<T>(
  resource: string,
  options: UseInfiniteListOptions,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: [
      "infinite-list",
      resource,
      options.filter ?? null,
      options.sort ?? null,
      options.limit ?? null,
    ] as QueryKey,
    queryFn: async ({ pageParam }: { pageParam: string | undefined }): Promise<PagedResponse<T>> => {
      const entry = await registry.resolve(resource);
      return client.get<PagedResponse<T>>(entry.listPath, {
        params: {
          ...flattenFilter(options.filter),
          ...(options.sort !== undefined ? { sort: options.sort } : {}),
          ...(options.limit !== undefined ? { limit: options.limit } : {}),
          ...(pageParam !== undefined ? { cursor: pageParam } : {}),
        },
      });
    },
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (lastPage: PagedResponse<T>) =>
      lastPage.meta.hasMore ? (lastPage.meta.cursor ?? undefined) : undefined,
  };
}

export function useInfiniteList<T>(resource: string, options: UseInfiniteListOptions = {}) {
  return useInfiniteQuery<PagedResponse<T>, Error, InfiniteData<PagedResponse<T>>, QueryKey, string | undefined>(
    createInfiniteListQueryOptions<T>(resource, options),
  );
}
