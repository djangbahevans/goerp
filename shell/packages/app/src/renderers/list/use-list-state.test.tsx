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
  it("extracts bracketed filter keys and the sort key, round-tripping back to the same search object", () => {
    const search = { "filter[is_active]": "true", "filter[type]": "person", sort: "-created_at", unrelated: 1 };
    const state = parseListSearch(search);

    expect(state).toEqual({ filter: { is_active: "true", type: "person" }, sort: "-created_at" });
    expect(listStateToSearch(state)).toEqual({
      "filter[is_active]": "true",
      "filter[type]": "person",
      sort: "-created_at",
    });
  });

  it("omits sort from the search object when unset", () => {
    expect(listStateToSearch({ filter: {}, sort: undefined })).toEqual({});
  });
});

function Probe({ embedded, defaultSort }: { embedded: boolean; defaultSort: string | undefined }) {
  const state = useListState(embedded, defaultSort);
  return (
    <div>
      <span data-testid="filter">{JSON.stringify(state.filter)}</span>
      <span data-testid="sort">{state.sort ?? ""}</span>
      <button type="button" onClick={() => state.setFilter("is_active", "true")}>
        set-filter
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
