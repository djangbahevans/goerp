import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { Package, ShoppingCart, Users } from "lucide-react";
import { ChromeSidebar } from "./chrome-sidebar.js";
import type { NavigationGroup } from "./navigation-types.js";
import type { SidebarStoreLike } from "./sidebar-store.js";

// A mocked navigation-tree fixture standing in for viewRegistry.navigationTree
// (unbuilt, goerp#575/#674 — see use-navigation-tree.ts) — the real per-module
// data this component will eventually consume unchanged.
const FIXTURE_TREE: NavigationGroup[] = [
  {
    key: "sales",
    label: "Sales",
    icon: ShoppingCart,
    module: "sales",
    children: [
      { key: "orders", label: "Orders", path: "/sales/orders", icon: ShoppingCart },
      {
        key: "invoices",
        label: "Invoices",
        path: "/sales/invoices",
        icon: ShoppingCart,
        badgeCountRoute: "/sales/invoices/pending-count",
      },
    ],
  },
  {
    key: "inventory",
    label: "Inventory",
    icon: Package,
    module: "inventory",
    children: [{ key: "products", label: "Products", path: "/inventory/products", icon: Package }],
  },
  {
    key: "hr",
    label: "HR",
    icon: Users,
    module: "hr",
    children: [{ key: "employees", label: "Employees", path: "/hr/employees", icon: Users }],
  },
];

// A fresh, story-local fake store — same shape as sidebar-store.test.tsx's
// fakeStore, reused here instead of the real localStorage-backed singleton
// so each story's initial state is deterministic and stories don't leak
// state into one another.
function fakeStore(initial: { collapsed: boolean; expandedGroups: string[] }): SidebarStoreLike {
  let state = { collapsed: initial.collapsed, expandedGroups: new Set(initial.expandedGroups) };
  const listeners = new Set<(s: typeof state) => void>();
  const notify = () => {
    for (const l of listeners) l(state);
  };
  return {
    getState: () => state,
    toggleCollapsed: () => {
      state = { ...state, collapsed: !state.collapsed };
      notify();
    },
    toggleGroup: (key: string) => {
      const expandedGroups = new Set(state.expandedGroups);
      if (expandedGroups.has(key)) expandedGroups.delete(key);
      else expandedGroups.add(key);
      state = { ...state, expandedGroups };
      notify();
    },
    subscribe: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}

// The global withPermissions decorator (preview.tsx) is always-allow, by
// design, for stories that don't care about permission filtering. This
// story does — it wants FIXTURE_TREE's groups filtered by a specific,
// visible set of enabled modules, matching chrome-header.stories.tsx's own
// precedent of building a local PermissionContext.Provider (from the same
// `@goerp/sdk/auth` import ChromeSidebar itself reads via
// use-navigation-tree.ts) rather than the global decorator's blanket
// permissive default.
const permissionValue = createPermissionContextValue({
  permissions: new Set<string>(),
  fieldAccess: {},
  modulesEnabled: new Set(["sales", "inventory", "hr"]),
});

function renderAt(store: SidebarStoreLike) {
  const queryClient = new QueryClient();
  queryClient.setQueryData(["nav-badge", "/sales/invoices/pending-count"], { count: 3 });

  const rootRoute = createRootRoute({
    component: () => (
      <PermissionContext.Provider value={permissionValue}>
        <QueryClientProvider client={queryClient}>
          <ChromeSidebar tree={FIXTURE_TREE} store={store} />
        </QueryClientProvider>
      </PermissionContext.Provider>
    ),
  });
  const routes = FIXTURE_TREE.flatMap((group) => group.children).map((item) =>
    createRoute({ getParentRoute: () => rootRoute, path: item.path, component: () => null }),
  );
  const router = createRouter({
    routeTree: rootRoute.addChildren(routes),
    history: createMemoryHistory({ initialEntries: ["/sales/orders"] }),
  });
  return <RouterProvider router={router} />;
}

const meta: Meta<typeof ChromeSidebar> = {
  title: "Chrome/ChromeSidebar",
  component: ChromeSidebar,
};

export default meta;

type Story = StoryObj<typeof ChromeSidebar>;

// Expanded rail, Sales open (showing the active "Orders" item and the
// "Invoices" badge count), Inventory and HR collapsed.
export const Expanded: Story = {
  render: () => renderAt(fakeStore({ collapsed: false, expandedGroups: ["sales"] })),
};

// Icon-only rail — group/item labels become accessible names instead of
// visible text, and hovering/focusing an item after 500ms reveals its label
// in a tooltip.
export const Collapsed: Story = {
  render: () => renderAt(fakeStore({ collapsed: true, expandedGroups: ["sales"] })),
};

// Expanded rail with every group collapsed — the default a first-time
// visitor sees before opening any group.
export const AllGroupsCollapsed: Story = {
  render: () => renderAt(fakeStore({ collapsed: false, expandedGroups: [] })),
};
