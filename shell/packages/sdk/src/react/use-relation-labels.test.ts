import { describe, expect, it, vi } from "vitest";
import type { APIClient, PagedResponse } from "../http/types.js";
import type { ResourceRegistry, ResourceRegistryEntry } from "../schema/index.js";
import type { RelationBatchSpec } from "./use-relation-labels.js";
import { createRelationLabelsQueryOptions, mergeLabelsByKey } from "./use-relation-labels.js";

function fakeRegistry(): Pick<ResourceRegistry, "resolve"> {
  return {
    resolve: vi.fn(
      async (): Promise<ResourceRegistryEntry> => ({
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
      }),
    ),
  };
}

function fakeClient(response: PagedResponse<unknown>): Pick<APIClient, "get"> {
  const get = vi.fn(async () => response);
  return { get } as unknown as Pick<APIClient, "get">;
}

describe("createRelationLabelsQueryOptions", () => {
  it("resolves the resource, dedupes/sorts ids, and maps id to labelField", async () => {
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
    );

    expect(options.enabled).toBe(true);
    const result = await options.queryFn();

    expect(registry.resolve).toHaveBeenCalledWith("contacts.contact");
    expect(client.get).toHaveBeenCalledWith("/contacts", { params: { "filter[id][]": ["a", "b"], limit: 2 } });
    expect(result).toEqual({ a: "Acme Inc", b: "Beta Corp" });
  });

  it("is disabled when there are no ids to look up", () => {
    const options = createRelationLabelsQueryOptions({
      key: "customer_id",
      resource: "contacts.contact",
      labelField: "customer_name",
      ids: [],
    });

    expect(options.enabled).toBe(false);
  });
});

describe("mergeLabelsByKey", () => {
  it("merges multiple specs sharing the same key instead of overwriting", () => {
    // list-renderer.tsx builds one spec per (column, already-fetched page) —
    // a second page's result must not clobber the first page's labels.
    const specs: RelationBatchSpec[] = [
      { key: "customer_id", resource: "contacts.contact", labelField: "name", ids: ["a"] },
      { key: "customer_id", resource: "contacts.contact", labelField: "name", ids: ["b"] },
    ];
    const results = [{ a: "Acme Inc" }, { b: "Beta Corp" }];

    expect(mergeLabelsByKey(specs, results)).toEqual(new Map([["customer_id", { a: "Acme Inc", b: "Beta Corp" }]]));
  });

  it("keeps distinct keys separate", () => {
    const specs: RelationBatchSpec[] = [
      { key: "customer_id", resource: "contacts.contact", labelField: "name", ids: ["a"] },
      { key: "owner_id", resource: "auth.user", labelField: "display_name", ids: ["u1"] },
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
    const specs: RelationBatchSpec[] = [
      { key: "customer_id", resource: "contacts.contact", labelField: "name", ids: ["a"] },
    ];

    expect(mergeLabelsByKey(specs, [undefined])).toEqual(new Map([["customer_id", {}]]));
  });
});
