import type { UseSavedFiltersResult } from "@goerp/sdk/react";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  computeDefaultFilters,
  listStateToSearch,
  parseListSearch,
  useDefaultFilterApplication,
  useListState,
} from "./use-list-state.js";

const { useSavedFiltersMock } = vi.hoisted(() => ({
  // No saved filters and already resolved by default — tests exercising
  // the saved-filter precedence itself override this per-test.
  useSavedFiltersMock: vi.fn(
    (): UseSavedFiltersResult => ({
      filters: [],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    }),
  ),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useSavedFilters: useSavedFiltersMock };
});

afterEach(() => {
  cleanup();
  useSavedFiltersMock.mockReset();
  useSavedFiltersMock.mockImplementation(() => ({
    filters: [],
    isLoading: false,
    save: vi.fn(),
    remove: vi.fn(),
    setDefault: vi.fn(),
  }));
});

describe("parseListSearch / listStateToSearch", () => {
  it("extracts bracketed filter keys, sort, and group_by, round-tripping back to the same search object", () => {
    const search = {
      "filter[is_active]": "true",
      "filter[type]": "person",
      sort: "-created_at",
      group_by: "state",
      unrelated: 1,
    };
    const state = parseListSearch(search);

    expect(state).toEqual({
      filter: { is_active: "true", type: "person" },
      sort: "-created_at",
      groupBy: "state",
    });
    expect(listStateToSearch(state)).toEqual({
      "filter[is_active]": "true",
      "filter[type]": "person",
      sort: "-created_at",
      group_by: "state",
    });
  });

  it("omits sort and group_by from the search object when unset", () => {
    expect(listStateToSearch({ filter: {}, sort: undefined, groupBy: undefined })).toEqual({});
  });

  it("round-trips a multi-value filter through the comma-joined [in] param", () => {
    const search = { "filter[tag_ids][in]": "01ja,01jb" };
    const state = parseListSearch(search);

    expect(state.filter).toEqual({ tag_ids: ["01ja", "01jb"] });
    expect(listStateToSearch(state)).toEqual({ "filter[tag_ids][in]": "01ja,01jb" });
  });

  it("normalizes a single-value [in] to a string even when the router coerced it to a number", () => {
    // TanStack Router's default parser turns a purely-numeric query value
    // into a real number — a single-element `in` list looks identical to
    // a lone numeric scalar until normalized back through String().
    expect(parseListSearch({ "filter[priority][in]": 5 }).filter).toEqual({ priority: ["5"] });
  });

  it("normalizes a numeric [like] value to a string", () => {
    expect(parseListSearch({ "filter[sku][like]": 42 }).filter).toEqual({ sku: { like: "42" } });
  });

  it("round-trips a range filter through separate [gte]/[lte] params", () => {
    const search = { "filter[created_at][gte]": "2026-01-01", "filter[created_at][lte]": "2026-02-01" };
    const state = parseListSearch(search);

    expect(state.filter).toEqual({ created_at: { gte: "2026-01-01", lte: "2026-02-01" } });
    expect(listStateToSearch(state)).toEqual({
      "filter[created_at][gte]": "2026-01-01",
      "filter[created_at][lte]": "2026-02-01",
    });
  });

  it("supports one-sided ranges", () => {
    const state = parseListSearch({ "filter[created_at][gte]": "2026-01-01" });
    expect(state.filter).toEqual({ created_at: { gte: "2026-01-01" } });
    expect(listStateToSearch(state)).toEqual({ "filter[created_at][gte]": "2026-01-01" });
  });

  it("round-trips an isnull filter (goerp#791)", () => {
    const state = parseListSearch({ "filter[parent_id][isnull]": true });
    expect(state.filter).toEqual({ parent_id: { isnull: true } });
    expect(listStateToSearch(state)).toEqual({ "filter[parent_id][isnull]": true });
  });

  it("round-trips isnull: false", () => {
    const state = parseListSearch({ "filter[parent_id][isnull]": false });
    expect(state.filter).toEqual({ parent_id: { isnull: false } });
    expect(listStateToSearch(state)).toEqual({ "filter[parent_id][isnull]": false });
  });

  it("round-trips a text filter through the %-wrapped [like] param, unwrapping for display", () => {
    const search = { "filter[name][like]": "%acme%" };
    const state = parseListSearch(search);

    expect(state.filter).toEqual({ name: { like: "acme" } });
    expect(listStateToSearch(state)).toEqual({ "filter[name][like]": "%acme%" });
  });

  it("omits an empty multi-value filter from the serialized search", () => {
    expect(listStateToSearch({ filter: { tag_ids: [] }, sort: undefined, groupBy: undefined })).toEqual({});
  });
});

function Probe({ embedded, defaultSort }: { embedded: boolean; defaultSort: string | undefined }) {
  const state = useListState(embedded, defaultSort);
  return (
    <div>
      <span data-testid="filter">{JSON.stringify(state.filter)}</span>
      <span data-testid="sort">{state.sort ?? ""}</span>
      <span data-testid="group-by">{state.groupBy ?? ""}</span>
      <button type="button" onClick={() => state.setFilter("is_active", "true")}>
        set-filter
      </button>
      <button type="button" onClick={() => state.setFilters({ is_active: "true", type: "person" })}>
        set-filters
      </button>
      <button type="button" onClick={() => state.setGroupBy("state")}>
        set-group-by
      </button>
    </div>
  );
}

async function renderListState(initialPath: string, embedded: boolean, defaultSort: string | undefined) {
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => <Probe embedded={embedded} defaultSort={defaultSort} />,
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  });
  await router.load();
  await act(async () => {
    render(<RouterProvider router={router} />);
  });
  return { router };
}

describe("useListState", () => {
  it("full-page mode: reads filter/sort from the URL and falls back to the view's default_sort", async () => {
    await renderListState("/?filter[is_active]=true&sort=-created_at", false, "name");

    expect(screen.getByTestId("filter").textContent).toBe(JSON.stringify({ is_active: true }));
    expect(screen.getByTestId("sort").textContent).toBe("-created_at");
  });

  it("full-page mode: falls back to default_sort when the URL has no sort param", async () => {
    await renderListState("/", false, "name");

    expect(screen.getByTestId("sort").textContent).toBe("name");
  });

  it("full-page mode: setFilter navigates, writing the filter into the URL", async () => {
    const { router } = await renderListState("/", false, undefined);

    await act(async () => {
      fireEvent.click(screen.getByText("set-filter"));
    });

    expect(router.state.location.search).toEqual({ "filter[is_active]": "true" });
    expect(screen.getByTestId("filter").textContent).toBe(JSON.stringify({ is_active: "true" }));
  });

  it("full-page mode: setFilters applies several fields in one navigation", async () => {
    const { router } = await renderListState("/", false, undefined);

    await act(async () => {
      fireEvent.click(screen.getByText("set-filters"));
    });

    expect(router.state.location.search).toEqual({ "filter[is_active]": "true", "filter[type]": "person" });
  });

  it("full-page mode: setFilter preserves unrelated search params already in the URL", async () => {
    const { router } = await renderListState("/?tab=activity", false, undefined);

    await act(async () => {
      fireEvent.click(screen.getByText("set-filter"));
    });

    expect(router.state.location.search).toEqual({ tab: "activity", "filter[is_active]": "true" });
  });

  it("full-page mode: setGroupBy navigates, writing group_by into the URL", async () => {
    const { router } = await renderListState("/", false, undefined);

    await act(async () => {
      fireEvent.click(screen.getByText("set-group-by"));
    });

    expect(router.state.location.search).toEqual({ group_by: "state" });
    expect(screen.getByTestId("group-by").textContent).toBe("state");
  });

  it("embedded mode: keeps state local and never touches the URL", async () => {
    const { router } = await renderListState("/", true, "name");

    expect(screen.getByTestId("sort").textContent).toBe("name");

    await act(async () => {
      fireEvent.click(screen.getByText("set-filter"));
    });

    expect(screen.getByTestId("filter").textContent).toBe(JSON.stringify({ is_active: "true" }));
    expect(router.state.location.search).toEqual({});
  });
});

function DefaultFilterProbe({
  embedded,
  defaultFilters,
  applySortAndGroupBy,
}: {
  embedded: boolean;
  defaultFilters?: Record<string, unknown>;
  applySortAndGroupBy?: boolean;
}) {
  const listState = useListState(embedded, undefined);
  useDefaultFilterApplication(
    { name: "probe_view", ...(defaultFilters !== undefined ? { default_filters: defaultFilters } : {}) },
    listState,
    embedded,
    applySortAndGroupBy,
  );
  return (
    <div>
      <span data-testid="filter">{JSON.stringify(listState.filter)}</span>
      <span data-testid="sort">{listState.sort ?? ""}</span>
      <span data-testid="group-by">{listState.groupBy ?? ""}</span>
    </div>
  );
}

async function renderDefaultFilterProbe(
  initialPath: string,
  embedded: boolean,
  options: { defaultFilters?: Record<string, unknown>; applySortAndGroupBy?: boolean } = {},
) {
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <DefaultFilterProbe
        embedded={embedded}
        {...(options.defaultFilters !== undefined ? { defaultFilters: options.defaultFilters } : {})}
        {...(options.applySortAndGroupBy !== undefined ? { applySortAndGroupBy: options.applySortAndGroupBy } : {})}
      />
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: [initialPath] }),
  });
  await router.load();
  await act(async () => {
    render(<RouterProvider router={router} />);
  });
  return { router };
}

describe("useDefaultFilterApplication", () => {
  it("applies the manifest's default_filters when nothing else is present", async () => {
    await renderDefaultFilterProbe("/", false, { defaultFilters: { is_active: true } });

    expect(screen.getByTestId("filter").textContent).toBe(JSON.stringify({ is_active: true }));
  });

  it("applies the user's own is_default saved filter instead of the manifest's default_filters", async () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [
        { id: "f1", viewName: "probe_view", label: "Mine", queryString: "?filter[is_active]=false", isDefault: true },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    await renderDefaultFilterProbe("/", false, { defaultFilters: { is_active: true } });

    expect(screen.getByTestId("filter").textContent).toBe(JSON.stringify({ is_active: false }));
  });

  it("an explicit URL filter wins over the user's own is_default saved filter", async () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [
        { id: "f1", viewName: "probe_view", label: "Mine", queryString: "?filter[is_active]=false", isDefault: true },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    await renderDefaultFilterProbe("/?filter[type]=company", false, {});

    expect(screen.getByTestId("filter").textContent).toBe(JSON.stringify({ type: "company" }));
  });

  it("waits for the saved-filters fetch to resolve before applying the manifest's default_filters", async () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [],
      isLoading: true,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    await renderDefaultFilterProbe("/", false, { defaultFilters: { is_active: true } });

    expect(screen.getByTestId("filter").textContent).toBe(JSON.stringify({}));
  });

  it("disables the saved-filters fetch when embedded, since it's never consulted there", async () => {
    await renderDefaultFilterProbe("/", true, { defaultFilters: { is_active: true } });

    expect(useSavedFiltersMock).toHaveBeenCalledWith("probe_view", { enabled: false });
    expect(screen.getByTestId("filter").textContent).toBe(JSON.stringify({ is_active: true }));
  });

  it("with applySortAndGroupBy, replays a saved default's sort and group_by too", async () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [
        { id: "f1", viewName: "probe_view", label: "Mine", queryString: "?sort=-name&group_by=state", isDefault: true },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    await renderDefaultFilterProbe("/", false, { applySortAndGroupBy: true });

    expect(screen.getByTestId("sort").textContent).toBe("-name");
    expect(screen.getByTestId("group-by").textContent).toBe("state");
  });

  it("without applySortAndGroupBy, a saved default's sort is never replayed (Kanban/Pivot never read listState.sort)", async () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [{ id: "f1", viewName: "probe_view", label: "Mine", queryString: "?sort=-name", isDefault: true }],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    await renderDefaultFilterProbe("/", false, {});

    expect(screen.getByTestId("sort").textContent).toBe("");
  });

  it("with applySortAndGroupBy, an explicit URL sort with no filter[...] params still wins over a saved default's sort", async () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [{ id: "f1", viewName: "probe_view", label: "Mine", queryString: "?sort=name", isDefault: true }],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });

    await renderDefaultFilterProbe("/?sort=-created_at", false, { applySortAndGroupBy: true });

    expect(screen.getByTestId("sort").textContent).toBe("-created_at");
  });
});

describe("computeDefaultFilters", () => {
  it("coerces default_filters' plain values, arrays becoming string arrays", () => {
    const defaults = computeDefaultFilters({ default_filters: { is_active: true, count: 3, tag_ids: [1, 2] } });
    expect(defaults).toEqual({ is_active: true, count: 3, tag_ids: ["1", "2"] });
  });

  it("wraps a text filter's default in {like}", () => {
    const defaults = computeDefaultFilters({
      filters: [{ field: "name", label: "Name", type: "text", default: "acme" }],
    });
    expect(defaults).toEqual({ name: { like: "acme" } });
  });

  it("passes a daterange/number_range filter's default through as {gte,lte}", () => {
    const defaults = computeDefaultFilters({
      filters: [{ field: "created_at", label: "Created", type: "daterange", default: { gte: "2026-01-01" } }],
    });
    expect(defaults).toEqual({ created_at: { gte: "2026-01-01" } });
  });

  it("stringifies a number_range filter's numeric default bounds", () => {
    const defaults = computeDefaultFilters({
      filters: [{ field: "amount", label: "Amount", type: "number_range", default: { gte: 10, lte: 100 } }],
    });
    expect(defaults).toEqual({ amount: { gte: "10", lte: "100" } });
  });

  it("accepts a range-shaped default_filters value even with no per-field type context", () => {
    const defaults = computeDefaultFilters({ default_filters: { created_at: { gte: "2026-01-01" } } });
    expect(defaults).toEqual({ created_at: { gte: "2026-01-01" } });
  });

  it("coerces a multi_select/tags filter's array default to a string array", () => {
    const defaults = computeDefaultFilters({
      filters: [{ field: "tag_ids", label: "Tags", type: "tags", default: ["a", "b"] }],
    });
    expect(defaults).toEqual({ tag_ids: ["a", "b"] });
  });

  it("default_filters wins over a filter's own default for the same field", () => {
    const defaults = computeDefaultFilters({
      default_filters: { type: "company" },
      filters: [{ field: "type", label: "Type", type: "select", default: "person" }],
    });
    expect(defaults).toEqual({ type: "company" });
  });

  it("returns an empty object when neither default_filters nor any filter declares a default", () => {
    expect(computeDefaultFilters({ filters: [{ field: "type", label: "Type", type: "select" }] })).toEqual({});
  });
});
