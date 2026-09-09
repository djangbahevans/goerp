import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Home } from "lucide-react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NavItem } from "./nav-item.js";
import type { NavigationItem } from "./navigation-types.js";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn(async () => ({ count: 5 })) }));
vi.mock("@goerp/sdk", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk")>();
  return { ...actual, apiClient: { ...actual.apiClient, get: getMock } };
});

const ITEM: NavigationItem = { key: "orders", label: "Orders", path: "/sales/orders", icon: Home };
const OTHER_ITEM: NavigationItem = { key: "invoices", label: "Invoices", path: "/sales/invoices", icon: Home };

afterEach(cleanup);

// The router's current location decides which item is "active" — matches
// NavItem's real contract of leaning on Link's own active-route detection
// rather than a hand-rolled path comparison.
async function renderAt(currentPath: string, item: NavigationItem, collapsed: boolean) {
  const queryClient = new QueryClient();
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <NavItem item={item} collapsed={collapsed} />
      </QueryClientProvider>
    ),
  });
  const routes = [ITEM, OTHER_ITEM].map((i) =>
    createRoute({ getParentRoute: () => rootRoute, path: i.path, component: () => null }),
  );
  const router = createRouter({
    routeTree: rootRoute.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [currentPath] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("NavItem", () => {
  it("renders the label as visible text when expanded", async () => {
    await renderAt(ITEM.path, ITEM, false);
    expect(screen.getByText("Orders")).toBeTruthy();
  });

  it("carries the label as aria-label instead of visible text when collapsed", async () => {
    await renderAt(ITEM.path, ITEM, true);
    expect(screen.queryByText("Orders")).toBeNull();
    expect(screen.getByRole("link", { name: "Orders" })).toBeTruthy();
  });

  it("marks the item aria-current=page when the router's current location matches its path", async () => {
    await renderAt(ITEM.path, ITEM, false);
    expect(screen.getByRole("link").getAttribute("aria-current")).toBe("page");
  });

  it("does not mark aria-current when the router's current location is a different item's path", async () => {
    await renderAt(OTHER_ITEM.path, ITEM, false);
    expect(screen.getByRole("link").getAttribute("aria-current")).toBeNull();
  });

  it("shows a collapsed-mode tooltip only after the 500ms hover delay", async () => {
    vi.useFakeTimers();
    try {
      await renderAt(ITEM.path, ITEM, true);
      const link = screen.getByRole("link");
      fireEvent.mouseEnter(link);

      expect(screen.queryByRole("tooltip")).toBeNull();
      act(() => vi.advanceTimersByTime(499));
      expect(screen.queryByRole("tooltip")).toBeNull();
      act(() => vi.advanceTimersByTime(1));
      expect(screen.getByRole("tooltip").textContent).toBe("Orders");
    } finally {
      vi.useRealTimers();
    }
  });

  it("hides the tooltip immediately on mouse leave, even before the delay elapses", async () => {
    vi.useFakeTimers();
    try {
      await renderAt(ITEM.path, ITEM, true);
      const link = screen.getByRole("link");
      fireEvent.mouseEnter(link);
      fireEvent.mouseLeave(link);
      act(() => vi.advanceTimersByTime(500));
      expect(screen.queryByRole("tooltip")).toBeNull();
    } finally {
      vi.useRealTimers();
    }
  });

  it("renders the item's badge count", async () => {
    await renderAt(ITEM.path, { ...ITEM, badgeCountRoute: "/sales/orders/pending-count" }, false);
    await waitFor(() => expect(screen.getByText("5")).toBeTruthy());
  });

  it("collapsed: nests the icon and its badge in a shared wrapper, not the full-width row", async () => {
    await renderAt(ITEM.path, { ...ITEM, badgeCountRoute: "/sales/orders/pending-count" }, true);
    const badge = await screen.findByText("5");
    const icon = screen.getByRole("link").querySelector("svg");
    // The badge's absolute positioning is relative to its nearest
    // "relative" ancestor — it must be the small icon+badge wrapper, not
    // the full-row Link/outer div, or it anchors to the row's edge instead
    // of the icon's corner.
    expect(badge.parentElement).toBe(icon?.parentElement);
    expect(badge.parentElement?.className).toContain("relative");
  });

  it("clears the pending tooltip timer on unmount", async () => {
    vi.useFakeTimers();
    const clearTimeoutSpy = vi.spyOn(globalThis, "clearTimeout");
    try {
      await renderAt(ITEM.path, ITEM, true);
      fireEvent.mouseEnter(screen.getByRole("link"));
      clearTimeoutSpy.mockClear();
      cleanup();
      expect(clearTimeoutSpy).toHaveBeenCalled();
    } finally {
      clearTimeoutSpy.mockRestore();
      vi.useRealTimers();
    }
  });
});
