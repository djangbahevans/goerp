import { createInfiniteListQueryOptions } from "@goerp/sdk/react";
import type { ViewDeclaration } from "@goerp/sdk/schema";
import { InfiniteQueryObserver, QueryClient } from "@tanstack/react-query";
import { describe, expect, it, vi } from "vitest";
import { configureViewPrefetch, prefetchViewData } from "./prefetch-view-data.js";

const RESOURCE = "contacts.contact";

function prefetchedKeys(declaration: ViewDeclaration, search: Record<string, unknown> = {}, recordId?: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  prefetchViewData(queryClient, { declaration, ...(recordId !== undefined ? { recordId } : {}) }, search);
  return {
    queryClient,
    keys: queryClient
      .getQueryCache()
      .getAll()
      .map((query) => query.queryKey),
  };
}

describe("prefetchViewData", () => {
  it("starts a list's first page with its URL filter, root-only tree filter and default sort", () => {
    const { keys } = prefetchedKeys(
      {
        name: "contacts",
        type: "list",
        resource: RESOURCE,
        label: "Contacts",
        default_sort: "name",
        default_sort_dir: "desc",
        tree_field: "parent_id",
      },
      { "filter[status]": "active" },
    );
    expect(keys).toEqual([
      ["infinite-list", null, RESOURCE, { status: "active", parent_id: { isnull: true } }, "-name", null],
    ]);
  });

  it("prefers the URL's sort over the list's default sort", () => {
    const { keys } = prefetchedKeys(
      { name: "contacts", type: "list", resource: RESOURCE, label: "Contacts", default_sort: "name" },
      { sort: "-created_at" },
    );
    expect(keys).toEqual([["infinite-list", null, RESOURCE, {}, "-created_at", null]]);
  });

  it("starts a kanban's first page without a sort", () => {
    const { keys } = prefetchedKeys(
      {
        name: "pipeline",
        type: "kanban",
        resource: RESOURCE,
        label: "Pipeline",
        group_by: "stage",
        card_fields: ["name"],
      },
      { "filter[owner]": "u1", sort: "name" },
    );
    expect(keys).toEqual([["infinite-list", null, RESOURCE, { owner: "u1" }, null, null]]);
  });

  it("starts a form's record request, and nothing for a create form", () => {
    const form: ViewDeclaration = { name: "contact", type: "form", resource: RESOURCE, label: "Contact" };
    expect(prefetchedKeys(form, {}, "42").keys).toEqual([["record", RESOURCE, "42"]]);
    expect(prefetchedKeys(form).keys).toEqual([]);
  });

  it("leaves views whose first request depends on render-time state to their renderer", () => {
    const { keys } = prefetchedKeys({
      name: "calendar",
      type: "calendar",
      resource: RESOURCE,
      label: "Calendar",
      date_field: "due",
    });
    expect(keys).toEqual([]);
  });

  it("skips a declaration its renderer would reject", () => {
    const { keys } = prefetchedKeys({
      name: "contacts",
      type: "list",
      resource: RESOURCE,
      label: "Contacts",
      default_sort_dir: "sideways",
    } as ViewDeclaration);
    expect(keys).toEqual([]);
  });

  it("does not refetch data already in the cache", () => {
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const key = ["record", RESOURCE, "42"];
    queryClient.setQueryData(key, { id: "42" });

    prefetchViewData(
      queryClient,
      { declaration: { name: "contact", type: "form", resource: RESOURCE, label: "Contact" }, recordId: "42" },
      {},
    );

    expect(queryClient.getQueryState(key)?.fetchStatus).toBe("idle");
  });
});

describe("configureViewPrefetch", () => {
  const page = { data: [], meta: { hasMore: false, cursor: null } };

  function mountListObserver(queryClient: QueryClient, queryFn: () => Promise<typeof page>) {
    const observer = new InfiniteQueryObserver(queryClient, {
      ...createInfiniteListQueryOptions(RESOURCE, { filter: {} }),
      queryFn,
    });
    return observer.subscribe(() => {});
  }

  it("lets a renderer mounting right after the prefetch reuse its data", () => {
    const queryClient = new QueryClient();
    configureViewPrefetch(queryClient);
    const { queryKey } = createInfiniteListQueryOptions(RESOURCE, { filter: {} });
    queryClient.setQueryData(queryKey, { pages: [page], pageParams: [undefined] });
    const queryFn = vi.fn(async () => page);

    const unsubscribe = mountListObserver(queryClient, queryFn);

    expect(queryFn).not.toHaveBeenCalled();
    unsubscribe();
  });

  it("still refetches invalidated data on mount", async () => {
    const queryClient = new QueryClient();
    configureViewPrefetch(queryClient);
    const { queryKey } = createInfiniteListQueryOptions(RESOURCE, { filter: {} });
    queryClient.setQueryData(queryKey, { pages: [page], pageParams: [undefined] });
    await queryClient.invalidateQueries({ queryKey });
    const queryFn = vi.fn(async () => page);

    const unsubscribe = mountListObserver(queryClient, queryFn);

    expect(queryFn).toHaveBeenCalledTimes(1);
    unsubscribe();
  });
});
