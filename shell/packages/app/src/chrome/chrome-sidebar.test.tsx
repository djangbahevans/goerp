import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import { Home } from "lucide-react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ChromeSidebar } from "./chrome-sidebar.js";
import type { NavigationGroup } from "./navigation-types.js";

const FIXTURE_TREE: NavigationGroup[] = [
  { key: "sales", label: "Sales", icon: Home, module: "sales", children: [] },
  { key: "hr", label: "HR", icon: Home, module: "hr", children: [] },
];

let sidebarState = { collapsed: false, expandedGroups: new Set<string>() };
const toggleGroup = vi.fn();

vi.mock("./use-navigation-tree.js", () => ({ useNavigationTree: () => FIXTURE_TREE }));
vi.mock("./sidebar-store.js", () => ({
  useSidebar: () => ({ ...sidebarState, toggleCollapsed: vi.fn(), toggleGroup }),
}));
vi.mock("./nav-group.js", () => ({
  NavGroupSection: ({ group }: { group: NavigationGroup }) => <span>{group.label}</span>,
}));

afterEach(cleanup);

async function renderSidebar() {
  const rootRoute = createRootRoute({ component: () => <ChromeSidebar /> });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("ChromeSidebar", () => {
  it("renders a Main navigation landmark composing one entry per top-level nav group", async () => {
    await renderSidebar();
    expect(screen.getByRole("navigation", { name: "Main" })).toBeTruthy();
    expect(screen.getByText("Sales")).toBeTruthy();
    expect(screen.getByText("HR")).toBeTruthy();
  });

  it("renders at the expanded width by default", async () => {
    sidebarState = { collapsed: false, expandedGroups: new Set() };
    await renderSidebar();
    expect(screen.getByRole("navigation", { name: "Main" }).style.width).toBe("var(--sidebar-width)");
  });

  it("renders at the collapsed width when the store reports collapsed", async () => {
    sidebarState = { collapsed: true, expandedGroups: new Set() };
    await renderSidebar();
    expect(screen.getByRole("navigation", { name: "Main" }).style.width).toBe("var(--sidebar-collapsed-width)");
  });
});
