import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Decorator } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import {
  type FakeNotificationTemplatesOptions,
  installFakeNotificationTemplatesBackend,
} from "./fake-notification-templates-backend.js";
import { FIXTURE_TYPES, fixtureTemplates } from "./notification-templates-fixtures.js";

const me = {
  id: "me",
  email: "ada@acme.test",
  name: "Ada Admin",
  contactId: null,
  avatarUrl: null,
  roles: ["admin"],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  passwordChangeRequired: false,
  passwordMinLength: 12,
  phone: null,
  title: null,
  theme: "system" as const,
  contrast: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};
const tenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en", "fr", "pt-BR"],
  passwordMinLength: 12,
};
const auth: AuthContextValue = {
  state: { status: "authenticated", user: me, tenant },
  isAuthenticated: true,
  user: me,
  tenant,
  login: async () => null,
  completeHandoff: async () => {},
  selectTenant: async () => null,
  logout: async () => {},
  submitMFA: async () => {},
  updateProfile: async () => {},
  updatePreferences: async () => {},
  changePassword: async () => {},
  reloadSession: async () => {},
  expireSession: () => {},
};

// A story's beforeEach: installs the in-memory backend and returns its cleanup.
export function fakeTemplatesBackend(options: Partial<FakeNotificationTemplatesOptions> = {}) {
  return () =>
    installFakeNotificationTemplatesBackend({ types: FIXTURE_TYPES, templates: fixtureTemplates(), ...options })
      .restore;
}

// Auth as a tenant admin whose tenant offers English, French and Brazilian
// Portuguese, a router for links, and a fresh query client per story.
export const withTemplatesShell: Decorator = (Story) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const permissions = createPermissionContextValue({
    permissions: new Set(),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  const rootRoute = createRootRoute({
    component: () => (
      <QueryClientProvider client={queryClient}>
        <AuthContext.Provider value={auth}>
          <PermissionContext.Provider value={permissions}>
            <Story />
          </PermissionContext.Provider>
        </AuthContext.Provider>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return <RouterProvider router={router} />;
};
