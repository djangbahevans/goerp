import type { PagedResponse } from "@goerp/sdk";
import { AuthContext } from "@goerp/sdk/auth";
import { localeStore } from "@goerp/sdk/i18n";
import type { Notification } from "@goerp/sdk/notifications";
import { buildEmptyViewRegistry, ViewRegistryContext } from "@goerp/sdk/schema";
import type { Decorator, Meta, StoryObj } from "@storybook/react-vite";
import { type InfiniteData, QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { useEffect } from "react";
import { ChromeLayout } from "./chrome-layout.js";
import type { NavigationGroup } from "./navigation-types.js";

const fakeUser = {
  id: "u1",
  email: "jane.doe@example.com",
  name: "Jane Doe",
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
  login: async () => null,
  completeHandoff: async () => {},
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
};

const TREE: NavigationGroup[] = [
  {
    key: "sales",
    label: "Sales",
    icon: "shopping-cart",
    module: "sales",
    children: [
      { key: "orders", label: "Orders", path: "/sales/orders", icon: "shopping-cart" },
      { key: "invoices", label: "Invoices", path: "/sales/invoices", icon: "shopping-cart" },
    ],
  },
  {
    key: "hr",
    label: "HR",
    icon: "users",
    module: "hr",
    children: [{ key: "employees", label: "Employees", path: "/hr/employees", icon: "users" }],
  },
];

const registry = { ...buildEmptyViewRegistry(), navigationTree: TREE };

function PageContent() {
  return (
    <div className="flex flex-col gap-4 p-6">
      <h1 className="font-semibold text-xl">Orders</h1>
      {Array.from({ length: 40 }, (_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: static filler rows.
        <p key={i} className="text-text-secondary">
          Order ORD-{String(i + 1).padStart(4, "0")}
        </p>
      ))}
    </div>
  );
}

const withShell: Decorator = (Story) => {
  const queryClient = new QueryClient();
  const emptyPage: PagedResponse<Notification> = { data: [], meta: { cursor: null, hasMore: false } };
  const infinite: InfiniteData<PagedResponse<Notification>> = { pages: [emptyPage], pageParams: [undefined] };
  queryClient.setQueryData(["notifications", 20], infinite);
  queryClient.setQueryData(["notifications", "unread-count"], { count: 2 });

  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={fakeAuth}>
          <ViewRegistryContext.Provider value={registry}>
            <Story />
          </ViewRegistryContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>
    ),
  });
  const routes = TREE.flatMap((group) => group.children).map((item) =>
    createRoute({ getParentRoute: () => rootRoute, path: item.path, component: PageContent }),
  );
  const router = createRouter({
    routeTree: rootRoute.addChildren(routes),
    history: createMemoryHistory({ initialEntries: ["/sales/orders"] }),
  });
  return <RouterProvider router={router} />;
};

function withLocale(locale: string): Decorator {
  return (Story) => {
    useEffect(() => {
      localeStore.setLocale(locale);
      return () => localeStore.setLocale("en");
    }, []);
    return <Story />;
  };
}

const meta: Meta<typeof ChromeLayout> = {
  title: "Chrome/ChromeLayout",
  component: ChromeLayout,
  parameters: { layout: "fullscreen" },
  decorators: [withShell],
};

export default meta;

type Story = StoryObj<typeof ChromeLayout>;

export const LeftToRight: Story = { decorators: [withLocale("en")] };

export const RightToLeft: Story = { decorators: [withLocale("ar")] };
