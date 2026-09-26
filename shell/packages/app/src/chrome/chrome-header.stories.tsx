import type { PagedResponse } from "@goerp/sdk";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Notification } from "@goerp/sdk/notifications";
import { type MyScheduledActivity, myScheduledActivitiesQueryKey } from "@goerp/sdk/react";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { type InfiniteData, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { MY_ACTIVITIES_PAGE_SIZE } from "../activities/use-due-activity-count.js";
import { ChromeHeader } from "./chrome-header.js";

const fakeUser = {
  id: "u1",
  email: "jane.doe@example.com",
  name: null,
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const fakeTenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};
const fakeAuth = {
  state: { status: "authenticated" as const, user: fakeUser, tenant: fakeTenant },
  isAuthenticated: true,
  user: fakeUser,
  tenant: fakeTenant,
  login: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
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
  // Three overdue or due-today activities for the user menu's badge.
  const today = new Date().toISOString().slice(0, 10);
  const due: PagedResponse<MyScheduledActivity> = {
    data: [1, 2, 3].map((n) => ({
      id: `s${n}`,
      model: "sales.order",
      recordId: `o${n}`,
      type: "call",
      summary: `Activity ${n}`,
      note: null,
      dueDate: today,
      assignee: { id: "u1", name: null, avatarUrl: null },
      createdBy: { id: "u1", name: null, avatarUrl: null },
      createdAt: `${today}T08:00:00Z`,
      doneAt: null,
      doneBy: null,
      feedback: null,
      recordName: `SO-000${n}`,
    })),
    meta: { cursor: null, hasMore: false },
  };
  queryClient.setQueryData([...myScheduledActivitiesQueryKey(), MY_ACTIVITIES_PAGE_SIZE], {
    pages: [due],
    pageParams: [undefined],
  });

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
