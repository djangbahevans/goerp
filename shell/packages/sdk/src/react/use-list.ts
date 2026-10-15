import { hashKey, type Query, type QueryKey, useQuery } from "@tanstack/react-query";
import { useCallback, useState } from "react";
import { listResource } from "../api/resource-api.js";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { FilterOf, RecordOf, ResourceName, ResourceRef, SortOf } from "../names.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";
import { useLiveListQueries } from "./use-live-list-queries.js";

const defaultLimit = 50;

export interface ListOptions<R extends ResourceName = ResourceName> {
  filter?: FilterOf<R>;
  sort?: SortOf<R> | SortOf<R>[];
  limit?: number;
  cursor?: string;
  enabled?: boolean;
  staleTime?: number;
  select?: (data: PagedResponse<RecordOf<R>>) => PagedResponse<RecordOf<R>>;
  // Defaults to the resource's feed:{module}; null subscribes to nothing.
  realtimeChannel?: string | null;
}

export interface UseListResult<R extends ResourceName> {
  data: PagedResponse<RecordOf<R>> | undefined;
  isLoading: boolean;
  isError: boolean;
  error: Error | null;
  cursor: string | undefined;
  hasMore: boolean;
  nextPage: () => void;
  prevPage: () => void;
  refetch: () => void;
}

// Prefixes the query key of every page of the resource's list, whatever its filter, sort and cursor.
export function listQueryKey(resource: string): QueryKey {
  return ["list", resource];
}

type ListQueryParams = {
  [K in "filter" | "sort" | "limit" | "cursor"]?: ListOptions<ResourceRef>[K] | undefined;
};

function createListQueryOptions<T>(
  resource: string,
  params: ListQueryParams,
  registry: Pick<ResourceRegistry, "resolve"> = resourceRegistry,
  client: Pick<APIClient, "get"> = apiClient,
) {
  const limit = params.limit ?? defaultLimit;
  return {
    queryKey: [
      ...listQueryKey(resource),
      params.filter ?? null,
      params.sort ?? null,
      limit,
      params.cursor ?? null,
    ] as QueryKey,
    queryFn: (): Promise<PagedResponse<T>> =>
      listResource<T>(
        resource,
        {
          ...(params.filter !== undefined ? { filter: params.filter } : {}),
          ...(params.sort !== undefined ? { sort: params.sort } : {}),
          limit,
          ...(params.cursor !== undefined ? { cursor: params.cursor } : {}),
        },
        registry,
        client,
      ),
  };
}

function matchesListQuery(query: Query, resource: string): boolean {
  return query.queryKey[0] === "list" && query.queryKey[1] === resource;
}

// Every key part except the trailing cursor: keys sharing it are pages of the same list.
function pagedListOf(key: QueryKey): string {
  return hashKey(key.slice(0, -1));
}

export function useList<R extends ResourceName>(resource: R, options: ListOptions<NoInfer<R>> = {}): UseListResult<R> {
  type T = RecordOf<R>;
  const { filter, sort, cursor: initialCursor, enabled = true, staleTime, select, realtimeChannel } = options;
  const limit = options.limit ?? defaultLimit;

  const query = hashKey([resource, filter, sort, limit, initialCursor]);
  let [history, setHistory] = useState({ query, cursors: [initialCursor] });
  if (history.query !== query) {
    history = { query, cursors: [initialCursor] };
    setHistory(history);
  }
  const cursor = history.cursors.at(-1);

  useLiveListQueries(resource, matchesListQuery, { channel: realtimeChannel, enabled });

  const queryOptions = createListQueryOptions<T>(resource, { filter, sort, limit, cursor });
  const result = useQuery<PagedResponse<T>, Error>({
    ...queryOptions,
    enabled,
    // Keeps the current page on screen while another page of the same list loads.
    placeholderData: (previous, previousQuery) =>
      previousQuery !== undefined && pagedListOf(previousQuery.queryKey) === pagedListOf(queryOptions.queryKey)
        ? previous
        : undefined,
    ...(staleTime !== undefined ? { staleTime } : {}),
    ...(select !== undefined ? { select } : {}),
  });

  const page = result.isPlaceholderData ? undefined : result.data;
  const next = page?.meta.hasMore ? (page.meta.cursor ?? undefined) : undefined;

  const nextPage = useCallback(() => {
    if (next === undefined) return;
    setHistory((current) =>
      current.query === query && current.cursors.at(-1) === cursor
        ? { query, cursors: [...current.cursors, next] }
        : current,
    );
  }, [query, cursor, next]);

  const prevPage = useCallback(() => {
    setHistory((current) =>
      current.query === query && current.cursors.length > 1
        ? { query, cursors: current.cursors.slice(0, -1) }
        : current,
    );
  }, [query]);

  const { refetch: refetchQuery } = result;
  const refetch = useCallback(() => {
    void refetchQuery();
  }, [refetchQuery]);

  return {
    data: result.data,
    isLoading: result.isLoading,
    isError: result.isError,
    error: result.error,
    cursor,
    hasMore: page?.meta.hasMore ?? false,
    nextPage,
    prevPage,
    refetch,
  };
}
