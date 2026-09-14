import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { PivotViewDeclaration } from "./pivot-manifest-types.js";
import { PivotRenderer } from "./pivot-renderer.js";

const { usePivotDataMock } = vi.hoisted(() => ({ usePivotDataMock: vi.fn() }));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, usePivotData: usePivotDataMock };
});

afterEach(() => {
  cleanup();
  usePivotDataMock.mockReset();
});

const view: PivotViewDeclaration = {
  name: "sales_pivot",
  type: "pivot",
  resource: "sales.order",
  label: "Sales Analysis",
  rows: ["region"],
  columns: ["state"],
  values: [{ field: "amount_total", aggregation: "sum", label: "Revenue" }],
};

async function renderPivotRenderer(
  props: { embedded?: boolean; baseFilter?: Record<string, string>; recordId?: string } = {},
  viewOverride: PivotViewDeclaration = view,
) {
  // useListState calls useSearch/useNavigate unconditionally even in
  // embedded mode (its own doc comment) — a router context is always
  // needed, matching list-renderer.test.tsx's own harness.
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const permissionValue = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <QueryClientProvider client={queryClient}>
        <PermissionContext.Provider value={permissionValue}>
          <PivotRenderer view={viewOverride} module="sales" {...props} />
        </PermissionContext.Provider>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("PivotRenderer", () => {
  it("shows a loading state while the pivot data is in flight", async () => {
    usePivotDataMock.mockReturnValue({ data: undefined, isLoading: true, isFetching: true, isError: false });

    await renderPivotRenderer();

    expect(document.querySelector('[data-skeleton="table"]')).toBeTruthy();
  });

  it("shows an error state with a retry action", async () => {
    const refetch = vi.fn();
    usePivotDataMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isFetching: false,
      isError: true,
      error: new Error("boom"),
      refetch,
    });

    await renderPivotRenderer();

    expect(screen.getByRole("alert").textContent).toContain("boom");
    screen.getByText("Retry").click();
    expect(refetch).toHaveBeenCalled();
  });

  it("maps a loaded response into the pivot grid and shows the title", async () => {
    usePivotDataMock.mockReturnValue({
      data: {
        cells: [
          { row: ["east"], column: ["confirmed"], values: { amount_total_sum: 150 } },
          { row: ["east"], column: [null], values: { amount_total_sum: 150 } },
        ],
      },
      isLoading: false,
      isFetching: false,
      isError: false,
    });

    await renderPivotRenderer();

    expect(screen.getByRole("heading", { name: "Sales Analysis" })).toBeTruthy();
    expect(screen.getByText("east")).toBeTruthy();
    expect(screen.getByText("confirmed")).toBeTruthy();
    expect(screen.getByText("150")).toBeTruthy();
  });

  it("shows the empty state when the response has no cells", async () => {
    usePivotDataMock.mockReturnValue({ data: { cells: [] }, isLoading: false, isFetching: false, isError: false });

    await renderPivotRenderer();

    expect(screen.getByRole("heading", { name: "No sales analysis data found." })).toBeTruthy();
  });

  it("passes rows/columns/values and the merged filter to usePivotData", async () => {
    usePivotDataMock.mockReturnValue({ data: { cells: [] }, isLoading: false, isFetching: false, isError: false });

    await renderPivotRenderer({ embedded: true, baseFilter: { customer_id: "acme" } });

    expect(usePivotDataMock).toHaveBeenCalledWith(
      "sales.order",
      expect.objectContaining({
        rows: ["region"],
        columns: ["state"],
        values: [{ field: "amount_total", aggregation: "sum", label: "Revenue" }],
        filter: expect.objectContaining({ customer_id: "acme" }),
        cacheKeyPrefix: "embedded::sales_pivot",
      }),
    );
  });

  it("applies default_filters once on mount", async () => {
    usePivotDataMock.mockReturnValue({ data: { cells: [] }, isLoading: false, isFetching: false, isError: false });

    await renderPivotRenderer({ embedded: true }, { ...view, default_filters: { state: "confirmed" } });

    expect(usePivotDataMock).toHaveBeenCalledWith(
      "sales.order",
      expect.objectContaining({ filter: expect.objectContaining({ state: "confirmed" }) }),
    );
  });
});
