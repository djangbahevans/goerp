import { describe, expect, it, vi } from "vitest";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { BatchLoaderRegistry, ResourceMetadataEntry, ResourceMetadataRegistry } from "../schema/index.js";
import type { RelationBatchSpec } from "./use-relation-labels.js";
import { createRelationLabelsQueryOptions, mergeLabelsByKey } from "./use-relation-labels.js";

function fakeRegistry(overrides: Partial<ResourceMetadataEntry> = {}): Pick<ResourceMetadataRegistry, "resolve"> {
  return {
    resolve: vi.fn(
      async (): Promise<ResourceMetadataEntry> => ({
        module: "contacts",
        resource: "contacts.contact",
        listRoute: "GET /contacts",
        getRoute: "GET /contacts/{id}",
        defaultListView: "contacts_list",
        defaultFormView: "contacts_form",
        labelField: "display_name",
        searchParam: "q",
        fields: [],
        ...overrides,
      }),
    ),
  };
}

function fakeClient(response: PagedResponse<unknown>): Pick<APIClient, "get"> {
  const get = vi.fn(async () => response);
  return { get } as unknown as Pick<APIClient, "get">;
}

function noBatchLoaders(): Pick<BatchLoaderRegistry, "has" | "resolve"> {
  return { has: vi.fn(() => false), resolve: vi.fn() };
}

describe("createRelationLabelsQueryOptions", () => {
  it("resolves the resource, dedupes/sorts ids, and maps id to an explicit labelField override", async () => {
    const registry = fakeRegistry();
    const client = fakeClient({
      data: [
        { id: "b", customer_name: "Beta Corp" },
        { id: "a", customer_name: "Acme Inc" },
      ],
      meta: { cursor: null, hasMore: false },
    });
    const options = createRelationLabelsQueryOptions(
      { key: "customer_id", resource: "contacts.contact", labelField: "customer_name", ids: ["a", "b", "a"] },
      registry,
      client,
      noBatchLoaders(),
    );

    expect(options.enabled).toBe(true);
    const result = await options.queryFn();

    expect(registry.resolve).toHaveBeenCalledWith("contacts.contact");
    expect(client.get).toHaveBeenCalledWith("/contacts", {
      params: { "filter[id][in]": "a,b", limit: 2 },
    });
    expect(result).toEqual({ a: "Acme Inc", b: "Beta Corp" });
  });

  it("falls back to the registry's labelField when the spec doesn't override it", async () => {
    const registry = fakeRegistry({ labelField: "display_name" });
    const client = fakeClient({
      data: [{ id: "a", display_name: "Acme Inc" }],
      meta: { cursor: null, hasMore: false },
    });
    const options = createRelationLabelsQueryOptions(
      { key: "customer_id", resource: "contacts.contact", ids: ["a"] },
      registry,
      client,
      noBatchLoaders(),
    );

    expect(await options.queryFn()).toEqual({ a: "Acme Inc" });
  });

  it("resolves to no labels, without erroring, when the resource is unregistered", async () => {
    const registry: Pick<ResourceMetadataRegistry, "resolve"> = { resolve: vi.fn(async () => undefined) };
    const client = fakeClient({ data: [], meta: { cursor: null, hasMore: false } });
    const options = createRelationLabelsQueryOptions(
      { key: "customer_id", resource: "x.y", ids: ["a"] },
      registry,
      client,
      noBatchLoaders(),
    );

    expect(await options.queryFn()).toEqual({});
    expect(client.get).not.toHaveBeenCalled();
  });

  it("resolves to no labels, without requesting an empty path, when the resource has no list route", async () => {
    const registry = fakeRegistry({ listRoute: "" });
    const client = fakeClient({ data: [], meta: { cursor: null, hasMore: false } });
    const options = createRelationLabelsQueryOptions(
      { key: "customer_id", resource: "contacts.contact", ids: ["a"] },
      registry,
      client,
      noBatchLoaders(),
    );

    expect(await options.queryFn()).toEqual({});
    expect(client.get).not.toHaveBeenCalled();
  });

  it("uses the view+column's registered batch loader instead of the registry/auto-fetch when one exists", async () => {
    const registry = fakeRegistry();
    const client = fakeClient({ data: [], meta: { cursor: null, hasMore: false } });
    const loader = vi.fn(async (ids: string[]) => new Map(ids.map((id) => [id, `Loaded ${id}`])));
    const batchLoaders: Pick<BatchLoaderRegistry, "has" | "resolve"> = {
      has: vi.fn((key: string) => key === "sales.orders_list.customer_id"),
      resolve: vi.fn(() => loader),
    };
    const options = createRelationLabelsQueryOptions(
      { key: "customer_id", resource: "contacts.contact", view: "sales.orders_list", ids: ["b", "a"] },
      registry,
      client,
      batchLoaders,
    );

    expect(await options.queryFn()).toEqual({ a: "Loaded a", b: "Loaded b" });
    expect(loader).toHaveBeenCalledWith(["a", "b"]);
    expect(batchLoaders.resolve).toHaveBeenCalledWith("sales.orders_list.customer_id");
    expect(registry.resolve).not.toHaveBeenCalled();
    expect(client.get).not.toHaveBeenCalled();
  });

  it("doesn't collide when two different relation columns on the same view each have their own loader", async () => {
    const client = fakeClient({ data: [], meta: { cursor: null, hasMore: false } });
    const customerLoader = vi.fn(async (ids: string[]) => new Map(ids.map((id) => [id, `Customer ${id}`])));
    const salespersonLoader = vi.fn(async (ids: string[]) => new Map(ids.map((id) => [id, `Salesperson ${id}`])));
    const loaders = new Map([
      ["sales.orders_list.customer_id", customerLoader],
      ["sales.orders_list.salesperson_id", salespersonLoader],
    ]);
    const batchLoaders: Pick<BatchLoaderRegistry, "has" | "resolve"> = {
      has: vi.fn((key: string) => loaders.has(key)),
      resolve: vi.fn((key: string) => {
        const loader = loaders.get(key);
        if (!loader) throw new Error("missing");
        return loader;
      }),
    };

    const customerOptions = createRelationLabelsQueryOptions(
      { key: "customer_id", resource: "sales.customer", view: "sales.orders_list", ids: ["c1"] },
      fakeRegistry(),
      client,
      batchLoaders,
    );
    const salespersonOptions = createRelationLabelsQueryOptions(
      { key: "salesperson_id", resource: "auth.user", view: "sales.orders_list", ids: ["u1"] },
      fakeRegistry(),
      client,
      batchLoaders,
    );

    expect(await customerOptions.queryFn()).toEqual({ c1: "Customer c1" });
    expect(await salespersonOptions.queryFn()).toEqual({ u1: "Salesperson u1" });
    expect(customerLoader).toHaveBeenCalledWith(["c1"]);
    expect(salespersonLoader).toHaveBeenCalledWith(["u1"]);
  });

  it("is disabled when there are no ids to look up", () => {
    const options = createRelationLabelsQueryOptions(
      { key: "customer_id", resource: "contacts.contact", ids: [] },
      fakeRegistry(),
      undefined,
      noBatchLoaders(),
    );

    expect(options.enabled).toBe(false);
  });
});

describe("mergeLabelsByKey", () => {
  it("merges multiple specs sharing the same key instead of overwriting", () => {
    // list-renderer.tsx builds one spec per (column, already-fetched page) —
    // a second page's result must not clobber the first page's labels.
    const specs: RelationBatchSpec[] = [
      { key: "customer_id", resource: "contacts.contact", ids: ["a"] },
      { key: "customer_id", resource: "contacts.contact", ids: ["b"] },
    ];
    const results = [{ a: "Acme Inc" }, { b: "Beta Corp" }];

    expect(mergeLabelsByKey(specs, results)).toEqual(new Map([["customer_id", { a: "Acme Inc", b: "Beta Corp" }]]));
  });

  it("keeps distinct keys separate", () => {
    const specs: RelationBatchSpec[] = [
      { key: "customer_id", resource: "contacts.contact", ids: ["a"] },
      { key: "owner_id", resource: "auth.user", ids: ["u1"] },
    ];
    const results = [{ a: "Acme Inc" }, { u1: "Ada" }];

    expect(mergeLabelsByKey(specs, results)).toEqual(
      new Map([
        ["customer_id", { a: "Acme Inc" }],
        ["owner_id", { u1: "Ada" }],
      ]),
    );
  });

  it("treats an unresolved (still-loading) result as contributing no labels yet", () => {
    const specs: RelationBatchSpec[] = [{ key: "customer_id", resource: "contacts.contact", ids: ["a"] }];

    expect(mergeLabelsByKey(specs, [undefined])).toEqual(new Map([["customer_id", {}]]));
  });
});
