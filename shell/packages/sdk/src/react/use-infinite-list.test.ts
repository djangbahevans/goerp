import { describe, expect, it, vi } from "vitest";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { ResourceRegistry, ResourceRegistryEntry } from "../schema/index.js";
import { createInfiniteListQueryOptions } from "./use-infinite-list.js";

function fakeRegistry(entry: Partial<ResourceRegistryEntry> = {}): Pick<ResourceRegistry, "resolve"> {
  return {
    resolve: vi.fn(async () => ({
      module: "contacts",
      resource: "contacts.contact",
      listPath: "/contacts",
      getPath: "/contacts/{id}",
      createPath: "/contacts",
      updatePath: "/contacts/{id}",
      deletePath: null,
      listMethod: "GET",
      createMethod: "POST",
      updateMethod: "PUT",
      deleteMethod: null,
      ...entry,
    })),
  };
}

function fakeClient(response: PagedResponse<unknown>): Pick<APIClient, "get"> {
  const get = vi.fn(async () => response);
  return { get } as unknown as Pick<APIClient, "get">;
}

// `queryFn` is typed as possibly `skipToken` (a unique symbol) by
// react-query's overloads — narrowed away here since this hook never
// passes skipToken.
async function callQueryFn(
  options: ReturnType<typeof createInfiniteListQueryOptions>,
  pageParam: string | undefined,
): Promise<PagedResponse<unknown>> {
  const fn = options.queryFn as (ctx: {
    pageParam: string | undefined;
    queryKey: readonly unknown[];
    meta: undefined;
    direction: "forward";
  }) => Promise<PagedResponse<unknown>>;
  return fn({ pageParam, queryKey: options.queryKey!, meta: undefined, direction: "forward" });
}

describe("createInfiniteListQueryOptions", () => {
  it("resolves the resource and fetches its list path with flattened filter params", async () => {
    const registry = fakeRegistry();
    const client = fakeClient({ data: [], meta: { cursor: null, hasMore: false } });
    const options = createInfiniteListQueryOptions(
      "contacts.contact",
      { filter: { is_active: true }, sort: "-created_at", limit: 25 },
      registry,
      client,
    );

    await callQueryFn(options, undefined);

    expect(registry.resolve).toHaveBeenCalledWith("contacts.contact");
    expect(client.get).toHaveBeenCalledWith("/contacts", {
      params: { "filter[is_active]": true, sort: "-created_at", limit: 25 },
    });
  });

  it("sends the page param as a cursor on subsequent pages", async () => {
    const registry = fakeRegistry();
    const client = fakeClient({ data: [], meta: { cursor: null, hasMore: false } });
    const options = createInfiniteListQueryOptions("contacts.contact", {}, registry, client);

    await callQueryFn(options, "page-2");

    expect(client.get).toHaveBeenCalledWith("/contacts", { params: { cursor: "page-2" } });
  });

  it("advances to the next cursor only while hasMore is true", () => {
    const options = createInfiniteListQueryOptions(
      "contacts.contact",
      {},
      fakeRegistry(),
      fakeClient({ data: [], meta: { cursor: null, hasMore: false } }),
    );

    expect(options.getNextPageParam({ data: [], meta: { cursor: "next", hasMore: true } })).toBe("next");
    expect(options.getNextPageParam({ data: [], meta: { cursor: "next", hasMore: false } })).toBe(undefined);
  });
});
