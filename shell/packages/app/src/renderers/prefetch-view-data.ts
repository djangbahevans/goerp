import { createInfiniteListQueryOptions } from "@goerp/sdk/react";
import type { ResolvedView } from "@goerp/sdk/schema";
import { parseManifestValue } from "@goerp/sdk/schema";
import type { QueryClient } from "@tanstack/react-query";
import { createRecordQueryOptions } from "./form/use-form-record.js";
import { KanbanViewDeclarationSchema } from "./kanban/kanban-manifest-types.js";
import { ListViewDeclarationSchema } from "./list/list-view-types.js";
import { defaultSortOf, parseListSearch, treeRootFilter } from "./list/use-list-state.js";

// The router reruns loaders on invalidation; only an empty cache is filled.
const ONLY_WHEN_EMPTY = Number.POSITIVE_INFINITY;
const LIST_PAGE_FRESH_MS = 5_000;
const RECORD_FRESH_MS = 30_000;

// Lets a renderer mounting after the route loader's prefetch reuse its result. Invalidation and
// realtime updates still mark these queries stale immediately.
export function configureViewPrefetch(queryClient: QueryClient): void {
  queryClient.setQueryDefaults(["infinite-list"], { staleTime: LIST_PAGE_FRESH_MS });
  queryClient.setQueryDefaults(["record"], { staleTime: RECORD_FRESH_MS });
}

// Starts a full-page view's first request under its renderer's query key. Calendar, timeline and
// pivot requests depend on render-time state, so their renderers start them.
export function prefetchViewData(
  queryClient: QueryClient,
  view: Pick<ResolvedView, "declaration" | "recordId">,
  search: Record<string, unknown>,
): void {
  const { declaration } = view;
  if (declaration.type === "form") {
    if (view.recordId !== undefined) {
      void queryClient.prefetchQuery({
        ...createRecordQueryOptions(declaration.resource, view.recordId),
        staleTime: ONLY_WHEN_EMPTY,
      });
    }
    return;
  }

  const { filter, sort } = parseListSearch(search);
  if (declaration.type === "list") {
    const list = parseManifestValue(ListViewDeclarationSchema, declaration);
    if (!list.success) return;
    const listSort = sort ?? defaultSortOf(list.output);
    void queryClient.prefetchInfiniteQuery({
      ...createInfiniteListQueryOptions(list.output.resource, {
        filter: treeRootFilter(list.output, filter),
        ...(listSort !== undefined ? { sort: listSort } : {}),
      }),
      staleTime: ONLY_WHEN_EMPTY,
    });
  } else if (declaration.type === "kanban") {
    const kanban = parseManifestValue(KanbanViewDeclarationSchema, declaration);
    if (!kanban.success) return;
    void queryClient.prefetchInfiniteQuery({
      ...createInfiniteListQueryOptions(kanban.output.resource, { filter }),
      staleTime: ONLY_WHEN_EMPTY,
    });
  }
}
