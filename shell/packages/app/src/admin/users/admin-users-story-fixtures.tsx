import type { AuthContextValue } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { Decorator } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import {
  type FakeActivity,
  type FakeBackendOptions,
  type FakeSession,
  type FakeUser,
  installFakeAdminUsersBackend,
} from "./fake-admin-users-backend.js";

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
  availableLocales: ["en"],
  firstDayOfWeek: "monday" as const,
  numberFormat: "1,234.56" as const,
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

const ago = (hours: number) => new Date(Date.now() - hours * 3600 * 1000).toISOString();

export const STORY_USERS: FakeUser[] = [
  { id: "me", name: "Ada Admin", email: "ada@acme.test", roles: ["admin"], status: "active", lastLoginAt: ago(0.2) },
  {
    id: "u-bola",
    name: "Bola Mensah",
    email: "bola.mensah@acme.test",
    roles: ["portal", "user"],
    status: "active",
    lastLoginAt: ago(5),
    phone: "+233 20 555 0142",
    jobTitle: "Accountant",
  },
  {
    id: "u-chidi",
    name: "Chidi Okafor",
    email: "chidi.okafor@acme.test",
    roles: ["user"],
    status: "suspended",
    lastLoginAt: ago(24 * 12),
  },
  {
    id: "u-efua",
    name: "Efua Boateng",
    email: "efua.boateng@acme.test",
    roles: [],
    status: "invited",
    lastLoginAt: null,
    invitation: { id: "inv-efua", role: "user", expiresAt: ago(-24 * 5), createdAt: ago(48) },
  },
  {
    id: "u-dayo",
    name: "Dayo Adeyemi",
    email: "dayo.adeyemi@acme.test",
    roles: ["user"],
    status: "active",
    accountSuspended: true,
    lastLoginAt: ago(24 * 30),
  },
  { id: "u-kwame", name: null, email: "kwame@acme.test", roles: ["user"], status: "active", lastLoginAt: null },
];

export const STORY_SESSIONS: Record<string, FakeSession[]> = {
  "u-bola": [
    {
      id: "fam-laptop",
      userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 Version/17.5 Safari/605.1.15",
      ipAddress: "41.66.18.2",
      countryCode: "GH",
      signedInAt: ago(72),
      lastActiveAt: ago(1),
    },
    {
      id: "fam-phone",
      userAgent: "Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 Chrome/128.0.0.0 Mobile Safari/537.36",
      ipAddress: "102.176.4.9",
      countryCode: "GH",
      signedInAt: ago(200),
      lastActiveAt: ago(30),
    },
  ],
};

function activity(id: string, hours: number, overrides: Partial<FakeActivity>): FakeActivity {
  return {
    id,
    source: "auth",
    occurred_at: ago(hours),
    action: "login.success",
    success: true,
    failure_reason: null,
    actor: { id: "u-bola", name: "Bola Mensah" },
    user: { id: "u-bola", name: "Bola Mensah" },
    record: null,
    changed_fields: null,
    ip_address: "41.66.18.2",
    user_agent: "Safari",
    metadata: null,
    ...overrides,
  };
}

const RECORD_CHANGE = { source: "data", user: null, ip_address: null, user_agent: null } as const;

export const STORY_ACTIVITY: Record<string, FakeActivity[]> = {
  "u-bola": [
    activity("a1", 0.5, {
      ...RECORD_CHANGE,
      action: "record.updated",
      record: { model: "sales.Invoice", id: "0192f1c4-7a2e-7c3d-9b1a-5e8f2d4c6a10" },
      changed_fields: ["due_date", "notes", "status"],
    }),
    activity("a2", 1, {
      ...RECORD_CHANGE,
      action: "record.created",
      record: { model: "sales.Invoice", id: "0192f1c4-7a2e-7c3d-9b1a-5e8f2d4c6a10" },
    }),
    activity("a3", 1.2, {}),
    activity("a4", 1.3, { action: "login.failure", success: false, failure_reason: "bad_password", actor: null }),
    activity("a5", 26, {
      action: "role.granted",
      actor: { id: "me", name: "Ada Admin" },
      metadata: { role: "portal" },
    }),
    activity("a6", 50, {
      ...RECORD_CHANGE,
      action: "record.deleted",
      record: { model: null, id: "0192f0aa-11b2-7d4e-8c5f-6a7b8c9d0e1f" },
    }),
    activity("a7", 72, { action: "password.changed" }),
  ],
};

export function fakeBackend(options: Partial<FakeBackendOptions> = {}) {
  return () => {
    const backend = installFakeAdminUsersBackend({
      users: STORY_USERS,
      sessions: STORY_SESSIONS,
      activity: STORY_ACTIVITY,
      ...options,
    });
    return backend.restore;
  };
}

// Auth as the tenant admin "me", a router for navigation hooks, and a fresh
// query client per story so one story's cache can't leak into the next.
export const withAdminShell: Decorator = (Story) => {
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
