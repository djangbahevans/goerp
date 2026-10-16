import type { Query, QueryClient } from "@tanstack/react-query";

export function isInfiniteListQuery(query: Query, resource: string): boolean {
  return (
    (query.queryKey[0] === "infinite-list" && query.queryKey[2] === resource) ||
    (query.queryKey[0] === "tree-children" && query.queryKey[1] === resource)
  );
}

export function isPagedListQuery(query: Query, resource: string): boolean {
  return query.queryKey[0] === "list" && query.queryKey[1] === resource;
}

// Marks every cached list of the resource stale and refetches the mounted ones. A list that is not
// mounted holds no realtime subscription, so it misses the records.changed message for a write.
export function invalidateResourceLists(queryClient: QueryClient, resource: string): Promise<void> {
  return queryClient.invalidateQueries({
    predicate: (query) => isInfiniteListQuery(query, resource) || isPagedListQuery(query, resource),
  });
}
