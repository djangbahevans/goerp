import type { PagedResponse } from "@goerp/sdk";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Notification } from "@goerp/sdk/notifications";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { type InfiniteData, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { ChromeHeader } from "./chrome-header.js";

const fakeUser = { id: "u1", email: "jane.doe@example.com", roles: [], amr: [], mfaVerifiedAt: null };
const fakeTenant = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };
const fakeAuth = {
  state: { status: "authenticated" as const, user: fakeUser, tenant: fakeTenant },
  isAuthenticated: true,
  user: fakeUser,
  tenant: fakeTenant,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
};

const permissionValue = createPermissionContextValue({
  permissions: new Set<string>(),
  fieldAccess: {},
  modulesEnabled: new Set<string>(),
});

// A 4-level nested route tree so the breadcrumb's >3-level collapse is
// actually exercised — the real app currently has only one shallow route.
const withProviders: Decorator = (Story) => {
  const queryClient = new QueryClient();
  const emptyPage: PagedResponse<Notification> = { data: [], meta: { cursor: null, hasMore: false } };
  const infinite: InfiniteData<PagedResponse<Notification>> = { pages: [emptyPage], pageParams: [undefined] };
  queryClient.setQueryData(["notifications", 20], infinite);
  queryClient.setQueryData(["notifications", "unread-count"], { count: 2 });

  const rootRoute = createRootRoute({});
  const sales = createRoute({
    getParentRoute: () => rootRoute,
    path: "sales",
    staticData: { breadcrumb: "Sales" },
  });
  const orders = createRoute({
    getParentRoute: () => sales,
    path: "orders",
    staticData: { breadcrumb: "Orders" },
  });
  const detail = createRoute({
    getParentRoute: () => orders,
    path: "ORD-0042",
    staticData: { breadcrumb: "ORD-0042" },
  });
  const lineItems = createRoute({
    getParentRoute: () => detail,
    path: "line-items",
    staticData: { breadcrumb: "Line Items" },
    component: () => (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={fakeAuth}>
          <PermissionContext.Provider value={permissionValue}>
            <Story />
          </PermissionContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>
    ),
  });

  const router = createRouter({
    routeTree: rootRoute.addChildren([sales.addChildren([orders.addChildren([detail.addChildren([lineItems])])])]),
    history: createMemoryHistory({ initialEntries: ["/sales/orders/ORD-0042/line-items"] }),
  });
  return <RouterProvider router={router} />;
};

const meta: Meta<typeof ChromeHeader> = {
  title: "Chrome/ChromeHeader",
  component: ChromeHeader,
  decorators: [withProviders],
};

export default meta;

type Story = StoryObj<typeof ChromeHeader>;

export const Default: Story = {};
