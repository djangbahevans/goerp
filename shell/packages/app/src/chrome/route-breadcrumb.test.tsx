import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { RouteBreadcrumb } from "./route-breadcrumb.js";

afterEach(cleanup);

// Builds a single nested route chain (root -> segment[0] -> segment[1] ->
// ...), matching TanStack Router's real addChildren nesting convention —
// each segment is a relative path piece, not a full URL.
async function renderAt(path: string, segments: { segment: string; staticData?: { breadcrumb?: string } }[]) {
  const rootRoute = createRootRoute({ component: () => <RouteBreadcrumb /> });

  // biome-ignore lint/suspicious/noExplicitAny: TanStack Router's route generics don't unify across a dynamically-built chain; this is test-only plumbing.
  let parent: any = rootRoute;
  // biome-ignore lint/suspicious/noExplicitAny: see above.
  const chain: any[] = [];
  for (const def of segments) {
    const currentParent = parent;
    const route = createRoute({
      getParentRoute: () => currentParent,
      path: def.segment,
      staticData: def.staticData ?? {},
      component: () => null,
    });
    chain.push(route);
    parent = route;
  }

  // biome-ignore lint/suspicious/noExplicitAny: see above.
  let tree: any;
  for (let i = chain.length - 1; i >= 0; i--) {
    const route = chain[i];
    tree = tree ? route.addChildren([tree]) : route;
  }

  const routeTree = tree ? rootRoute.addChildren([tree]) : rootRoute.addChildren([]);
  const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: [path] }) });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("RouteBreadcrumb", () => {
  it("renders 'Home' for a real index route mounted at /", async () => {
    await renderAt("/", [{ segment: "/" }]);
    expect(screen.getByText("Home")).toBeTruthy();
  });

  it("falls back to a humanized last path segment when a route has no staticData.breadcrumb", async () => {
    await renderAt("/sales-orders", [{ segment: "sales-orders" }]);
    expect(screen.getByText("Sales Orders")).toBeTruthy();
  });

  it("uses staticData.breadcrumb when a route declares one", async () => {
    await renderAt("/orders", [{ segment: "orders", staticData: { breadcrumb: "Orders" } }]);
    expect(screen.getByText("Orders")).toBeTruthy();
  });

  it("marks the last crumb aria-current=page and renders it as non-interactive text", async () => {
    await renderAt("/sales/orders", [
      { segment: "sales", staticData: { breadcrumb: "Sales" } },
      { segment: "orders", staticData: { breadcrumb: "Orders" } },
    ]);
    const current = screen.getByText("Orders");
    expect(current.getAttribute("aria-current")).toBe("page");
    expect(current.tagName).toBe("SPAN");
  });

  it("renders non-last crumbs as real links", async () => {
    await renderAt("/sales/orders", [
      { segment: "sales", staticData: { breadcrumb: "Sales" } },
      { segment: "orders", staticData: { breadcrumb: "Orders" } },
    ]);
    const link = screen.getByText("Sales");
    expect(link.tagName).toBe("A");
  });

  it("collapses trails beyond 3 levels into first, ellipsis, last two", async () => {
    await renderAt("/a/b/c/d", [
      { segment: "a", staticData: { breadcrumb: "A" } },
      { segment: "b", staticData: { breadcrumb: "B" } },
      { segment: "c", staticData: { breadcrumb: "C" } },
      { segment: "d", staticData: { breadcrumb: "D" } },
    ]);
    // Exactly 4 crumbs: first (A) + last two (C, D) stay visible; only the
    // single one in between (B) collapses into the ellipsis.
    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.queryByText("B")).toBeNull();
    expect(screen.getByText("C")).toBeTruthy();
    expect(screen.getByText("D")).toBeTruthy();
    const ellipsis = screen.getByText("…");
    expect(ellipsis.getAttribute("aria-label")).toBe("Hidden breadcrumb levels: B");
  });

  it("does not collapse a trail of exactly 3 levels", async () => {
    await renderAt("/a/b/c", [
      { segment: "a", staticData: { breadcrumb: "A" } },
      { segment: "b", staticData: { breadcrumb: "B" } },
      { segment: "c", staticData: { breadcrumb: "C" } },
    ]);
    expect(screen.getByText("A")).toBeTruthy();
    expect(screen.getByText("B")).toBeTruthy();
    expect(screen.getByText("C")).toBeTruthy();
    expect(screen.queryByText("…")).toBeNull();
  });

  it("skips a pathless layout route's match instead of rendering a duplicate crumb", async () => {
    const rootRoute = createRootRoute({ component: () => <RouteBreadcrumb /> });
    // A pathless route (id, no path) contributes a match sharing its
    // child's pathname — e.g. an auth-gate or shared layout wrapper.
    const layoutRoute = createRoute({ getParentRoute: () => rootRoute, id: "_layout", component: () => null });
    const ordersRoute = createRoute({
      getParentRoute: () => layoutRoute,
      path: "orders",
      staticData: { breadcrumb: "Orders" },
      component: () => null,
    });
    const routeTree = rootRoute.addChildren([layoutRoute.addChildren([ordersRoute])]);
    const router = createRouter({ routeTree, history: createMemoryHistory({ initialEntries: ["/orders"] }) });
    await router.load();
    render(<RouterProvider router={router} />);

    expect(screen.getAllByText("Orders")).toHaveLength(1);
  });
});
