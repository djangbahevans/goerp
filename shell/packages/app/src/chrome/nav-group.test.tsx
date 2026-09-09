import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Home } from "lucide-react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NavGroupSection } from "./nav-group.js";
import type { NavigationGroup } from "./navigation-types.js";

const GROUP: NavigationGroup = {
  key: "sales",
  label: "Sales",
  icon: Home,
  module: "sales",
  children: [{ key: "orders", label: "Orders", path: "/sales/orders", icon: Home }],
};

afterEach(cleanup);

async function renderGroup(props: Partial<Parameters<typeof NavGroupSection>[0]> = {}) {
  const onToggle = props.onToggle ?? vi.fn();
  const rootRoute = createRootRoute({
    component: () => (
      <NavGroupSection group={GROUP} collapsed={false} expanded={false} onToggle={onToggle} {...props} />
    ),
  });
  const ordersRoute = createRoute({ getParentRoute: () => rootRoute, path: "sales/orders", component: () => null });
  const router = createRouter({
    routeTree: rootRoute.addChildren([ordersRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
  return { onToggle };
}

describe("NavGroupSection", () => {
  it("reflects the expanded prop as aria-expanded", async () => {
    await renderGroup({ expanded: true });
    expect(screen.getByRole("button").getAttribute("aria-expanded")).toBe("true");
  });

  it("calls onToggle with the group's key when clicked", async () => {
    const { onToggle } = await renderGroup();
    fireEvent.click(screen.getByRole("button"));
    expect(onToggle).toHaveBeenCalledWith("sales");
  });

  it("hides children until expanded", async () => {
    await renderGroup({ expanded: false });
    expect(screen.queryByText("Orders")).toBeNull();
  });

  it("renders children once expanded", async () => {
    await renderGroup({ expanded: true });
    expect(screen.getByText("Orders")).toBeTruthy();
  });

  it("hides the visible label and uses aria-label instead when collapsed", async () => {
    await renderGroup({ collapsed: true });
    expect(screen.queryByText("Sales")).toBeNull();
    expect(screen.getByRole("button", { name: "Sales" })).toBeTruthy();
  });

  it("applies the child-indent guide when expanded and not collapsed", async () => {
    await renderGroup({ expanded: true, collapsed: false });
    const link = screen.getByRole("link");
    // link's parent is NavItem's own "relative" wrapper; the indent guide
    // wraps that.
    expect(link.parentElement?.parentElement?.style.marginInlineStart).toBe("var(--space-4)");
  });

  it("drops the child-indent guide when the rail is collapsed — no room for it at 56px", async () => {
    await renderGroup({ expanded: true, collapsed: true });
    const link = screen.getByRole("link");
    expect(link.parentElement?.parentElement?.style.marginInlineStart).toBe("");
  });
});
