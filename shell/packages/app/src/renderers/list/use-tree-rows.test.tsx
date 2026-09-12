import type { APIClient, PagedResponse } from "@goerp/sdk";
import type { ResourceRegistry, ResourceRegistryEntry } from "@goerp/sdk/schema";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Row } from "./list-view-types.js";
import { buildTreeRows, createTreeChildrenQueryOptions, useTreeRows } from "./use-tree-rows.js";

const { resolveMock, getMock } = vi.hoisted(() => ({
  resolveMock: vi.fn(
    async (): Promise<ResourceRegistryEntry> => ({ listPath: "/categories" }) as ResourceRegistryEntry,
  ),
  getMock: vi.fn(async (): Promise<PagedResponse<Row>> => ({ data: [], meta: { cursor: null, hasMore: false } })),
}));
vi.mock("@goerp/sdk", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk")>();
  return { ...actual, apiClient: { ...actual.apiClient, get: getMock } };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, resourceRegistry: { ...actual.resourceRegistry, resolve: resolveMock } };
});

afterEach(() => {
  cleanup();
  resolveMock.mockClear();
  getMock.mockClear();
});

const root1: Row = { id: "r1", name: "Root 1" };
const root2: Row = { id: "r2", name: "Root 2" };
const child1a: Row = { id: "c1a", name: "Child 1a" };
const child1b: Row = { id: "c1b", name: "Child 1b" };
const grandchild1a1: Row = { id: "g1a1", name: "Grandchild" };

describe("buildTreeRows", () => {
  it("renders only root rows when nothing is expanded", () => {
    const result = buildTreeRows([root1, root2], new Map(), new Set(), new Set());
    expect(result).toEqual([
      {
        row: root1,
        depth: 0,
        isExpanded: false,
        isLoadingChildren: false,
        hasChildrenUnknown: true,
        hasChildren: false,
        hasError: false,
      },
      {
        row: root2,
        depth: 0,
        isExpanded: false,
        isLoadingChildren: false,
        hasChildrenUnknown: true,
        hasChildren: false,
        hasError: false,
      },
    ]);
  });

  it("splices an expanded row's fetched children in right after it, at depth+1", () => {
    const childrenByParentId = new Map([["r1", [child1a, child1b]]]);
    const result = buildTreeRows([root1, root2], childrenByParentId, new Set(["r1"]), new Set());
    expect(result.map((r) => [r.row.id, r.depth])).toEqual([
      ["r1", 0],
      ["c1a", 1],
      ["c1b", 1],
      ["r2", 0],
    ]);
    expect(result[0]).toMatchObject({
      isExpanded: true,
      hasChildrenUnknown: false,
      hasChildren: true,
      hasError: false,
    });
  });

  it("recurses into a grandchild when both the row and its child are expanded", () => {
    const childrenByParentId = new Map([
      ["r1", [child1a]],
      ["c1a", [grandchild1a1]],
    ]);
    const result = buildTreeRows([root1], childrenByParentId, new Set(["r1", "c1a"]), new Set());
    expect(result.map((r) => [r.row.id, r.depth])).toEqual([
      ["r1", 0],
      ["c1a", 1],
      ["g1a1", 2],
    ]);
  });

  it("keeps a collapsed row's already-fetched children out of the render list", () => {
    const childrenByParentId = new Map([["r1", [child1a]]]);
    const result = buildTreeRows([root1], childrenByParentId, new Set(), new Set());
    expect(result).toEqual([
      {
        row: root1,
        depth: 0,
        isExpanded: false,
        isLoadingChildren: false,
        hasChildrenUnknown: false,
        hasChildren: true,
        hasError: false,
      },
    ]);
  });

  it("marks a fetched-empty row as no longer having unknown children", () => {
    const childrenByParentId = new Map<string, Row[]>([["r1", []]]);
    const result = buildTreeRows([root1], childrenByParentId, new Set(["r1"]), new Set());
    expect(result).toEqual([
      {
        row: root1,
        depth: 0,
        isExpanded: true,
        isLoadingChildren: false,
        hasChildrenUnknown: false,
        hasChildren: false,
        hasError: false,
      },
    ]);
  });

  it("reports isLoadingChildren for a row whose children query is in flight", () => {
    const result = buildTreeRows([root1], new Map(), new Set(["r1"]), new Set(["r1"]));
    expect(result[0]).toMatchObject({
      isExpanded: true,
      isLoadingChildren: true,
      hasChildrenUnknown: true,
      hasChildren: false,
      hasError: false,
    });
  });

  it("skips a row with no string id entirely for expansion purposes", () => {
    const noId: Row = { name: "No id" };
    const result = buildTreeRows([noId], new Map(), new Set(), new Set());
    expect(result).toEqual([
      {
        row: noId,
        depth: 0,
        isExpanded: false,
        isLoadingChildren: false,
        hasChildrenUnknown: true,
        hasChildren: false,
        hasError: false,
      },
    ]);
  });

  it("marks a row as errored when its parent id is in errorIds", () => {
    const result = buildTreeRows([root1], new Map(), new Set(["r1"]), new Set(), new Set(["r1"]));
    expect(result[0]).toMatchObject({ isExpanded: true, hasError: true });
  });

  it("breaks a cyclic tree_field chain instead of recursing without bound", () => {
    // r1's own "children" loop back to r1 — corrupted/mis-migrated data,
    // not something the engine's own cycle check allows via normal writes.
    const childrenByParentId = new Map([["r1", [root1]]]);
    const result = buildTreeRows([root1], childrenByParentId, new Set(["r1"]), new Set());
    expect(result).toHaveLength(2);
    expect(result[1]).toMatchObject({ row: root1, depth: 1, hasChildrenUnknown: true });
  });
});

function fakeRegistry(listPath = "/categories"): Pick<ResourceRegistry, "resolve"> {
  return { resolve: vi.fn(async (): Promise<ResourceRegistryEntry> => ({ listPath }) as ResourceRegistryEntry) };
}

function fakeClient(data: Row[]): Pick<APIClient, "get"> {
  const get = vi.fn(async (): Promise<PagedResponse<Row>> => ({ data, meta: { cursor: null, hasMore: false } }));
  return { get } as unknown as Pick<APIClient, "get">;
}

describe("createTreeChildrenQueryOptions", () => {
  it("fetches with the tree field equal to the parent id, merged into the view's other filters/sort", async () => {
    const registry = fakeRegistry();
    const client = fakeClient([child1a]);
    const options = createTreeChildrenQueryOptions(
      { parentId: "r1", resource: "contacts.category", treeField: "parent_id", filter: { active: true }, sort: "name" },
      registry,
      client,
    );

    const result = await options.queryFn();

    expect(registry.resolve).toHaveBeenCalledWith("contacts.category");
    expect(client.get).toHaveBeenCalledWith("/categories", {
      params: { "filter[active]": true, "filter[parent_id]": "r1", sort: "name" },
    });
    expect(result).toEqual([child1a]);
  });
});

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useTreeRows", () => {
  it("fetches a row's children and shows them once toggleExpand is called", async () => {
    getMock.mockResolvedValue({ data: [child1a], meta: { cursor: null, hasMore: false } });

    const { result } = renderHook(() => useTreeRows("contacts.category", "parent_id", 0, [root1], {}, undefined), {
      wrapper,
    });

    expect(result.current.treeRows).toEqual([
      {
        row: root1,
        depth: 0,
        isExpanded: false,
        isLoadingChildren: false,
        hasChildrenUnknown: true,
        hasChildren: false,
        hasError: false,
      },
    ]);

    result.current.toggleExpand("r1");

    await waitFor(() => {
      expect(result.current.treeRows.map((r) => r.row.id)).toEqual(["r1", "c1a"]);
    });
    expect(getMock).toHaveBeenCalledWith("/categories", { params: { "filter[parent_id]": "r1" } });
    // One page per expanded node's own children fetch — root rows are the
    // caller's own concern, not included here.
    expect(result.current.fetchedPages).toEqual([[child1a]]);
  });

  it("auto-expands root rows up to default_expanded_depth", async () => {
    getMock.mockResolvedValue({ data: [child1a], meta: { cursor: null, hasMore: false } });

    const { result } = renderHook(() => useTreeRows("contacts.category", "parent_id", 1, [root1], {}, undefined), {
      wrapper,
    });

    await waitFor(() => {
      expect(result.current.treeRows.map((r) => r.row.id)).toEqual(["r1", "c1a"]);
    });
  });

  it("never re-expands an id the user has manually collapsed after an auto-expand", async () => {
    getMock.mockResolvedValue({ data: [child1a], meta: { cursor: null, hasMore: false } });

    const { result } = renderHook(() => useTreeRows("contacts.category", "parent_id", 1, [root1], {}, undefined), {
      wrapper,
    });

    await waitFor(() => {
      expect(result.current.treeRows.map((r) => r.row.id)).toEqual(["r1", "c1a"]);
    });

    result.current.toggleExpand("r1");
    await waitFor(() => {
      expect(result.current.treeRows.map((r) => r.row.id)).toEqual(["r1"]);
    });

    // Stays collapsed across a re-render — the auto-expand effect must not
    // fight the user's own toggle back open.
    expect(result.current.treeRows.map((r) => r.row.id)).toEqual(["r1"]);
  });

  it("resets expansion when the filter changes, instead of leaking a children query for a stale id forever", async () => {
    getMock.mockResolvedValue({ data: [child1a], meta: { cursor: null, hasMore: false } });

    const { result, rerender } = renderHook(
      ({ filter }: { filter: Record<string, boolean> }) =>
        useTreeRows("contacts.category", "parent_id", 0, [root1], filter, undefined),
      { wrapper, initialProps: { filter: {} } },
    );

    result.current.toggleExpand("r1");
    await waitFor(() => expect(result.current.treeRows.map((r) => r.row.id)).toEqual(["r1", "c1a"]));

    getMock.mockClear();
    rerender({ filter: { active: true } });

    await waitFor(() => expect(result.current.treeRows.map((r) => r.row.id)).toEqual(["r1"]));

    // No further calls once expansion has settled back to empty — a stale
    // "r1" children query doesn't keep re-firing on later renders.
    const callsAfterSettling = getMock.mock.calls.length;
    rerender({ filter: { active: true } });
    expect(getMock.mock.calls.length).toBe(callsAfterSettling);
  });
});
