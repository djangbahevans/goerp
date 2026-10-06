import type { PagedResponse } from "@goerp/sdk";
import { AuthContext, type AuthContextValue, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Notification } from "@goerp/sdk/notifications";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { type InfiniteData, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { expect, userEvent, waitFor, within } from "storybook/test";
import { addDays, todayIn } from "../activities/activity-dates.js";
import { installFakeMyActivities } from "../activities/fake-my-activities.js";
import { BottomNavBar } from "./bottom-nav-bar.js";
import { ChromeSidebar } from "./chrome-sidebar.js";
import type { NavigationGroup } from "./navigation-types.js";
import type { SidebarStoreLike } from "./sidebar-store.js";

// A mocked navigation-tree fixture standing in for viewRegistry.navigationTree
// (packages/sdk/src/schema/view-registry.ts) — the real per-module data this
// component consumes unchanged. `icon` is a name string (<Icon name={...}>),
// not a component reference — see NavigationItem's own doc comment.
const FIXTURE_TREE: NavigationGroup[] = [
  {
    key: "sales",
    label: "Sales",
    icon: "shopping-cart",
    module: "sales",
    children: [
      { key: "orders", label: "Orders", path: "/sales/orders", icon: "shopping-cart" },
      {
        key: "invoices",
        label: "Invoices",
        path: "/sales/invoices",
        icon: "shopping-cart",
        badgeCountRoute: "/sales/invoices/pending-count",
      },
    ],
  },
  {
    key: "inventory",
    label: "Inventory",
    icon: "package",
    module: "inventory",
    children: [{ key: "products", label: "Products", path: "/inventory/products", icon: "package" }],
  },
  {
    key: "hr",
    label: "HR",
    icon: "users",
    module: "hr",
    children: [{ key: "employees", label: "Employees", path: "/hr/employees", icon: "users" }],
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

// Below 768px the rail gives way to BottomNavBar. useDueActivityCount reads
// the timezone from AuthContext and pages GET /_meta/scheduled-activities/mine,
// served here by the same fake the My activities stories install.
const auth = {
  user: { timezone: null },
  tenant: { defaultTimezone: "UTC" },
} as unknown as AuthContextValue;

const PHONE_VIEWPORT = {
  viewport: { options: { phone360: { name: "Phone (360px)", styles: { width: "360px", height: "740px" } } } },
};

function renderBottomBar(path: string) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const emptyPage: PagedResponse<Notification> = { data: [], meta: { cursor: null, hasMore: false } };
  const feed: InfiniteData<PagedResponse<Notification>> = { pages: [emptyPage], pageParams: [undefined] };
  queryClient.setQueryData(["notifications", "feed", 20], feed);
  queryClient.setQueryData(["notifications", "unread-count"], { count: 5 });

  const rootRoute = createRootRoute({
    component: () => (
      <PermissionContext.Provider value={permissionValue}>
        <AuthContext.Provider value={auth}>
          <QueryClientProvider client={queryClient}>
            <BottomNavBar tree={FIXTURE_TREE} store={fakeStore({ collapsed: false, expandedGroups: ["sales"] })} />
          </QueryClientProvider>
        </AuthContext.Provider>
      </PermissionContext.Provider>
    ),
  });
  const paths = ["/", "/activities", ...FIXTURE_TREE.flatMap((group) => group.children).map((item) => item.path)];
  const routes = paths.map((routePath) =>
    createRoute({ getParentRoute: () => rootRoute, path: routePath, component: () => null }),
  );
  const router = createRouter({
    routeTree: rootRoute.addChildren(routes),
    history: createMemoryHistory({ initialEntries: [path] }),
  });
  return <RouterProvider router={router} />;
}

const today = todayIn("UTC");
const dueActivities = () =>
  installFakeMyActivities([
    { dueDate: addDays(today, -2) },
    { dueDate: today },
    { dueDate: today },
    { dueDate: addDays(today, 4) },
  ]).restore;

const bottomBarStory: Story = {
  parameters: { layout: "fullscreen", ...PHONE_VIEWPORT },
  globals: { viewport: { value: "phone360", isRotated: false } },
  beforeEach: dueActivities,
};

// Home current, with 3 overdue-or-due-today activities and 5 unread
// notifications badged.
export const BottomBar: Story = {
  ...bottomBarStory,
  render: () => renderBottomBar("/"),
  play: async ({ canvasElement }) => {
    const nav = within(canvasElement).getByRole("navigation", { name: "Main" });
    await waitFor(() => expect(within(nav).getByRole("link", { name: "Activities, 3 due" })).toBeInTheDocument());
    expect(within(nav).getByRole("button", { name: "Notifications, 5 unread" })).toBeInTheDocument();
  },
};

// On /activities, the Activities item takes the current-page treatment.
export const BottomBarActivitiesCurrent: Story = {
  ...bottomBarStory,
  render: () => renderBottomBar("/activities"),
  play: async ({ canvasElement }) => {
    const nav = within(canvasElement).getByRole("navigation", { name: "Main" });
    const activities = await within(nav).findByRole("link", { name: /^Activities/ });
    expect(activities).toHaveAttribute("aria-current", "page");
  },
};

// More open: the modal "Menu" sheet holding the full navigation tree.
export const BottomBarMoreOpen: Story = {
  ...bottomBarStory,
  render: () => renderBottomBar("/sales/orders"),
  play: async ({ canvasElement }) => {
    const nav = within(canvasElement).getByRole("navigation", { name: "Main" });
    await userEvent.click(within(nav).getByRole("button", { name: "More" }));
    const sheet = await within(document.body).findByRole("dialog", { name: "Menu" });
    expect(within(sheet).getByRole("link", { name: "Orders" })).toHaveAttribute("aria-current", "page");
  },
};
