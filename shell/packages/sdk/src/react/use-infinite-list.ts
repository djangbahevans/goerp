import { type InfiniteData, type Query, type QueryKey, useInfiniteQuery } from "@tanstack/react-query";
import { listResource } from "../api/resource-api.js";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { FilterOf, RecordOf, ResourceName, ResourceRef, SortOf } from "../names.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";
import { useLiveListQueries } from "./use-live-list-queries.js";

export interface UseInfiniteListOptions<R extends ResourceName = ResourceName> {
  filter?: FilterOf<R>;
  sort?: SortOf<R> | SortOf<R>[];
  limit?: number;
  // Separates embedded instances without changing the HTTP request.
  cacheKeyPrefix?: string;
}

// Prefixes the query key of every page set of the resource's list, whatever its filter and sort.
export function infiniteListQueryKey(resource: string, cacheKeyPrefix?: string): QueryKey {
  return ["infinite-list", cacheKeyPrefix ?? null, resource];
}

export function createInfiniteListQueryOptions<T>(
  resource: string,
  options: UseInfiniteListOptions<ResourceRef>,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  return {
    queryKey: [
      ...infiniteListQueryKey(resource, options.cacheKeyPrefix),
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

function matchesListQuery(query: Query, resource: string): boolean {
  return (
    (query.queryKey[0] === "infinite-list" && query.queryKey[2] === resource) ||
    (query.queryKey[0] === "tree-children" && query.queryKey[1] === resource)
  );
}

export function useInfiniteList<R extends ResourceName>(resource: R, options: UseInfiniteListOptions<NoInfer<R>> = {}) {
  type T = RecordOf<R>;
  useLiveListQueries(resource, matchesListQuery);
  return useInfiniteQuery<PagedResponse<T>, Error, InfiniteData<PagedResponse<T>>, QueryKey, string | undefined>(
    createInfiniteListQueryOptions<T>(resource, options),
  );
}
