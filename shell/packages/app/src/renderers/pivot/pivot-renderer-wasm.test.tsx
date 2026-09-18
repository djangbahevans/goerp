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

const { usePivotWasmDataMock, useSavedFiltersMock } = vi.hoisted(() => ({
  usePivotWasmDataMock: vi.fn(),
  useSavedFiltersMock: vi.fn(() => ({
    filters: [],
    isLoading: false,
    save: vi.fn(),
    remove: vi.fn(),
    setDefault: vi.fn(),
  })),
}));
vi.mock("./use-pivot-wasm-data.js", () => ({ usePivotWasmData: usePivotWasmDataMock }));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useSavedFilters: useSavedFiltersMock };
});

afterEach(() => {
  cleanup();
  usePivotWasmDataMock.mockReset();
  useSavedFiltersMock.mockReset();
  useSavedFiltersMock.mockImplementation(() => ({
    filters: [],
    isLoading: false,
    save: vi.fn(),
    remove: vi.fn(),
    setDefault: vi.fn(),
  }));
});

// use_wasm left unset — view-system.md §8's default is true.
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

describe("PivotRenderer (use_wasm: true, the default)", () => {
  it("shows a loading state while the dataset is being fetched/loaded", async () => {
    usePivotWasmDataMock.mockReturnValue({ data: undefined, isLoading: true, isError: false, error: null });

    await renderPivotRenderer();

    expect(document.querySelector('[data-skeleton="table"]')).toBeTruthy();
  });

  it("shows an error state with a retry action", async () => {
    const refetch = vi.fn();
    usePivotWasmDataMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
      error: new Error("boom"),
      refetch,
    });

    await renderPivotRenderer();

    expect(screen.getByRole("alert").textContent).toContain("boom");
    screen.getByText("Retry").click();
    expect(refetch).toHaveBeenCalled();
  });

  it("maps a loaded response into the pivot grid and shows the title, with no recompute indicator", async () => {
    usePivotWasmDataMock.mockReturnValue({
      data: {
        cells: [
          { row: ["east"], column: ["confirmed"], values: { amount_total_sum: 150 } },
          { row: ["east"], column: [null], values: { amount_total_sum: 150 } },
        ],
      },
      isLoading: false,
      isError: false,
      error: null,
    });

    await renderPivotRenderer();

    expect(screen.getByRole("heading", { name: "Sales Analysis" })).toBeTruthy();
    expect(screen.getByText("east")).toBeTruthy();
    expect(screen.getByText("confirmed")).toBeTruthy();
    expect(screen.getByText("150")).toBeTruthy();
  });

  it("shows the empty state when the response has no cells", async () => {
    usePivotWasmDataMock.mockReturnValue({ data: { cells: [] }, isLoading: false, isError: false, error: null });

    await renderPivotRenderer();

    expect(screen.getByRole("heading", { name: "No sales analysis data found." })).toBeTruthy();
  });

  it("passes rows/columns/values and the merged filter to usePivotWasmData", async () => {
    usePivotWasmDataMock.mockReturnValue({ data: { cells: [] }, isLoading: false, isError: false, error: null });

    await renderPivotRenderer({ embedded: true, baseFilter: { customer_id: "acme" } });

    expect(usePivotWasmDataMock).toHaveBeenCalledWith(
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
});
