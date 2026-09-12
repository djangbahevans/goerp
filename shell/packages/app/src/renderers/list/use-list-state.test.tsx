import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { listStateToSearch, parseListSearch, useListState } from "./use-list-state.js";

afterEach(cleanup);

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
