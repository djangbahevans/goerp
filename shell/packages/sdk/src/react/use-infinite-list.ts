import { type InfiniteData, type QueryKey, useInfiniteQuery } from "@tanstack/react-query";
import { type ListParams, listResource } from "../api/resource-api.js";
import type { FilterParamValue } from "../http/filter-params.js";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

// typescript-sdk-reference.md §5 `useInfiniteList`.
export interface UseInfiniteListOptions {
  filter?: Record<string, FilterParamValue>;
  sort?: string;
  limit?: number;
  // Extra query-key segments only — never sent as a request param.
  // view-system.md's embedded-rendering contract needs a cache key
  // prefixed `embedded:{parentRecordId}:{viewName}` so two embedded
  // instances of the same resource/filter don't share a cache entry.
  cacheKeyPrefix?: string;
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
      options.cacheKeyPrefix ?? null,
      resource,
      options.filter ?? null,
      options.sort ?? null,
      options.limit ?? null,
    ] as QueryKey,
    queryFn: ({ pageParam }: { pageParam: string | undefined }): Promise<PagedResponse<T>> =>
      listResource<T>(
        resource,
        {
          ...(options.filter !== undefined ? { filter: options.filter as NonNullable<ListParams<T>["filter"]> } : {}),
          ...(options.sort !== undefined ? { sort: options.sort } : {}),
          ...(options.limit !== undefined ? { limit: options.limit } : {}),
          ...(pageParam !== undefined ? { cursor: pageParam } : {}),
        },
        registry,
        client,
      ),
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
