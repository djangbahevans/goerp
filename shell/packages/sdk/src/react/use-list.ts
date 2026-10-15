import { hashKey, type QueryKey, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { listResource } from "../api/resource-api.js";
import type { AppError } from "../error/app-error.js";
import { apiClient } from "../http/index.js";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { FilterOf, RecordOf, ResourceName, ResourceRef, SortOf } from "../names.js";
import { subscribeLiveQueries } from "../realtime/live-query-subscriptions.js";
import type { ResourceRegistry } from "../schema/index.js";
import { resourceRegistry } from "../schema/index.js";

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
  error: AppError | null;
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

export function createListQueryOptions<T>(
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

export function useList<R extends ResourceName>(resource: R, options: ListOptions<NoInfer<R>> = {}): UseListResult<R> {
  type T = RecordOf<R>;
  const { filter, sort, limit, cursor: initialCursor, enabled = true, staleTime, select, realtimeChannel } = options;
  const queryClient = useQueryClient();

  // The cursors of the pages visited since the list's query last changed; the last one is shown.
  const query = hashKey([resource, filter, sort, limit, initialCursor]);
  let [history, setHistory] = useState({ query, cursors: [initialCursor] });
  if (history.query !== query) {
    history = { query, cursors: [initialCursor] };
    setHistory(history);
  }
  const { cursors } = history;
  const cursor = cursors.at(-1);

  useEffect(() => {
    if (!enabled || realtimeChannel === null) return;
    const matches = (key: QueryKey) => key[0] === "list" && key[1] === resource;
    if (realtimeChannel !== undefined) {
      return subscribeLiveQueries(queryClient, realtimeChannel, resource, (q) => matches(q.queryKey));
    }
    let cancelled = false;
    let unsubscribe: (() => void) | undefined;
    void resourceRegistry.resolve(resource).then(
      (entry) => {
        if (cancelled) return;
        unsubscribe = subscribeLiveQueries(queryClient, `feed:${entry.module}`, resource, (q) => matches(q.queryKey));
      },
      () => {},
    );
    return () => {
      cancelled = true;
      unsubscribe?.();
    };
  }, [queryClient, resource, enabled, realtimeChannel]);

  const result = useQuery<PagedResponse<T>, AppError, PagedResponse<T>>({
    ...createListQueryOptions<T>(resource, { filter, sort, limit, cursor }),
    enabled,
    ...(staleTime !== undefined ? { staleTime } : {}),
    ...(select !== undefined ? { select } : {}),
  });

  const next = result.data?.meta.hasMore ? (result.data.meta.cursor ?? undefined) : undefined;

  return {
    data: result.data,
    isLoading: result.isLoading,
    isError: result.isError,
    error: result.error,
    cursor,
    hasMore: result.data?.meta.hasMore ?? false,
    nextPage: () => {
      if (next === undefined) return;
      setHistory({ query, cursors: [...cursors, next] });
    },
    prevPage: () => {
      if (cursors.length < 2) return;
      setHistory({ query, cursors: cursors.slice(0, -1) });
    },
    refetch: () => {
      void result.refetch();
    },
  };
}
