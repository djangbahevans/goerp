import { type InfiniteData, type QueryKey, useInfiniteQuery } from "@tanstack/react-query";
import { listResource } from "../api/resource-api.js";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { FilterOf, RecordOf, ResourceName, ResourceRef, SortOf } from "../names.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

export interface UseInfiniteListOptions<R extends ResourceName = ResourceName> {
  filter?: FilterOf<R>;
  sort?: SortOf<R> | SortOf<R>[];
  limit?: number;
  // Separates embedded instances without changing the HTTP request.
  cacheKeyPrefix?: string;
}

export function createInfiniteListQueryOptions<T>(
  resource: string,
  options: UseInfiniteListOptions<ResourceRef>,
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
          ...(options.filter !== undefined ? { filter: options.filter } : {}),
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

export function useInfiniteList<R extends ResourceName>(resource: R, options: UseInfiniteListOptions<NoInfer<R>> = {}) {
  type T = RecordOf<R>;
  return useInfiniteQuery<PagedResponse<T>, Error, InfiniteData<PagedResponse<T>>, QueryKey, string | undefined>(
    createInfiniteListQueryOptions<T>(resource, options),
  );
}
